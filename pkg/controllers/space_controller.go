/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deccov1 "github.com/platform9/decco/api/v1beta2"
)

// SpaceReconciler reconciles a Space object
type SpaceReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces/status,verbs=get;update;patch

func (r *SpaceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("space", req.NamespacedName)

	// Lookup the current keys
	space := &deccov1.Space{}
	err := r.Client.Get(ctx, req.NamespacedName, space)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if object is being deleted
	// TODO(erwin) set status to deleting to indicate that it is waiting for dependent objects to be deleted.
	if !space.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("Ignoring object being deleted.")
		return ctrl.Result{}, nil
	}

	// Validate object
	err = space.Spec.Validate()
	if err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciling namespace")
	err = r.reconcileNamespace(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile namespace: %w", err)
	}

	log.Info("Reconciling networkpolicy")
	err = r.reconcileNetPolicy(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile networkpolicy: %w", err)
	}

	log.Info("Reconciling RBAC rules")
	err = r.reconcileRBAC(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile RBAC rules: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *SpaceReconciler) reconcileNamespace(ctx context.Context, space *deccov1.Space) error {
	err := r.Client.Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
			},
			Labels: map[string]string{
				"app":                "decco",
				"decco-space-rsc-ns": space.Name,
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *SpaceReconciler) reconcileNetPolicy(ctx context.Context, space *deccov1.Space) error {
	// note(erwin): from original code, not sure why this condition is here.
	if space.Spec.Project == "" {
		return nil
	}

	err := r.Client.Create(ctx, &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      space.Name,
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
			},
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			Ingress: []netv1.NetworkPolicyIngressRule{
				{
					From: []netv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"decco-project": deccov1.RESERVED_PROJECT_NAME,
								},
							},
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"decco-project": space.Spec.Project,
								},
							},
						},
					},
				},
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *SpaceReconciler) reconcileRBAC(ctx context.Context, space *deccov1.Space) error {
	perms := space.Spec.Permissions
	if perms == nil {
		return nil
	}

	// Ensure that the "space-creator" role is created.
	err := r.Client.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "space-creator",
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
			},
		},
		Rules: perms.Rules,
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create space-creator role: %s", err)
	}

	// Bind the space-creator role to the target subject defined in the space.
	err = r.Client.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "space-creator",
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
			},
		},
		Subjects: []rbacv1.Subject{
			perms.Subject,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "space-creator",
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create space-creator role binding: %s", err)
	}

	return nil
}



func (r *SpaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1.Space{}).
		Complete(r)
}

func (r *SpaceReconciler) getLoggerFor(namespacedName string) logr.Logger {
	return r.Log.WithValues("space", namespacedName)
}
