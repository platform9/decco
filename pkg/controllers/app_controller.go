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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/intstr"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	batchv1 "k8s.io/api/batch/v1"
	appsv1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
)

// AppReconciler reconciles a App object
type AppReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=decco.platform9.com,resources=apps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decco.platform9.com,resources=apps/status,verbs=get;update;patch

func (r *AppReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	// Re-implementing https://github.com/platform9/decco/blob/104a69d77c1a599643c7c1f11adc06c2d98d23a5/pkg/app/app.go#L219
	ctx := context.Background()
	log := r.Log.WithValues("app", req.NamespacedName)

	// Lookup the current keys
	app := &deccov1beta2.App{}
	err := r.Client.Get(ctx, req.NamespacedName, app)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if App is being deleted
	// TODO(josh) set status to deleting to indicate that it is waiting for dependent objects to be deleted.
	if !app.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("Ignoring App being deleted.")
		return ctrl.Result{}, nil
	}

	// Augment App based on other values present in the App Spec
	if err := prepareApp(app); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to prepare App")
	}

	log.Info("Reconciling serviceaccount, role, and rolebinding")
	err = r.reconcileRBAC(ctx, app)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile RBAC: %w", err)
	}

	log.Info("Reconciling endpoints")
	err = r.reconcileEndpoints(ctx, app)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile endpoints: %w", err)
	}

	log.Info("Reconciling deployment")
	err = r.reconcileDeployment(ctx, app)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile deployment: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *AppReconciler) reconcileEndpoints(ctx context.Context, app *deccov1beta2.App) error {
	if app.Spec.RunAsJob {
		return nil // no service endpoints for a job
	}
	for _, e := range app.Spec.Endpoints {
		// TODO(josh): handle stunnel creation later
		// containers, volumes, svcPort, tgtPort, err = ar.createStunnel(&e,
		// 	containers, volumes, stunnelIndex)
		// if err != nil {
		// 	f := "failed to create stunnel for endpoint '%s': %s"
		// 	return nil, nil, fmt.Errorf(f, e.Name, err)
		// }

		if err := r.reconcileSvc(ctx, app, &e); err != nil {
			f := "failed to create service for endpoint '%s': %s"
			return fmt.Errorf(f, e.Name, err)
		}
		// TODO(josh): Handle ingress reconciliation and DNS upserts
		// if err := ar.createHttpIngress(&e); err != nil {
		// 	f := "failed to create http ingress for endpoint '%s': %s"
		// 	return nil, nil, fmt.Errorf(f, e.Name, err)
		// }
		// if err := ar.createTcpIngress(&e, svcPort); err != nil {
		// 	f := "failed to create tcp ingress for endpoint '%s': %s"
		// 	return nil, nil, fmt.Errorf(f, e.Name, err)
		// }
		// err = ar.updateDns(&e, false)
		// if err != nil {
		// 	f := "failed to update dns for endpoint '%s': %s"
		// 	return nil, nil, fmt.Errorf(f, e.Name, err)
		// }
	}
	return nil
}

func (r *AppReconciler) reconcileSvc(ctx context.Context, app *deccov1beta2.App, e *deccov1beta2.EndpointSpec) error {
	labels := map[string]string{
		"decco-derived-from": "app",
		"decco-app":          app.Name,
	}
	if e.IsMetricsEndpoint {
		labels["monitoring-group"] = "decco"
	}

	svc := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e.Name,
			Namespace: app.Namespace,
			Labels:    labels,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(app, deccov1beta2.GroupVersion.WithKind("App")),
			},
		},
		// TODO(josh): add back in the stunnel tomfoolery
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: 443,
					Name: "https",
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: e.Port,
					},
				},
				{
					Port: 80,
					Name: "http",
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: e.Port,
					},
				},
				{
					Port: e.Port,
					Name: "self",
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: e.Port,
					},
				},
			},
			Selector: map[string]string{
				"decco-app": app.Name,
			},
		},
	}

	err := r.Client.Create(ctx, svc)
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *AppReconciler) reconcileDeployment(ctx context.Context, app *deccov1beta2.App) error {
	// TODO(josh): handle stunnel stuff, as well as egresses
	objMeta := metav1.ObjectMeta{
		Name: app.Name,
		Namespace: app.Namespace,
		Labels: map[string]string {
			"decco-derived-from": "app",
		},
		OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(app, deccov1beta2.GroupVersion.WithKind("App")),
		},
	}
	var err error
	if app.Spec.RunAsJob {
		backoffLimit := app.Spec.JobBackoffLimit
		err = r.Client.Create(ctx, &batchv1.Job{
			ObjectMeta: objMeta,
			Spec: batchv1.JobSpec{
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta {
						Name: app.Name,
						Namespace: app.Namespace,
						Labels: map[string]string {
							"app": "decco",
							"decco-app": app.Name,
						},
					},
					Spec: app.Spec.PodSpec,
				},
				BackoffLimit: &backoffLimit,
			},
		})
	} else {
		err = r.Client.Create(ctx, &appsv1.Deployment{
			ObjectMeta: objMeta,
			Spec: appsv1.DeploymentSpec{
				Replicas: &app.Spec.InitialReplicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string {
						"decco-app": app.Name,
					},
				},
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta {
						Name: app.Name,
						Annotations: map[string]string {
							"linkerd.io/inject": "enabled",
							// https://linkerd.io/2/features/protocol-detection/#configuring-protocol-detection
							// Skip linkerd proxy when making outbound connections to mysql
							"config.linkerd.io/skip-outbound-ports": "3306",
						},
						Labels: map[string]string {
							"app": "decco",
							"decco-app": app.Name,
						},
					},
					Spec: app.Spec.PodSpec,
				},
			},
		})
	}

	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}


func (r *AppReconciler) reconcileRBAC(ctx context.Context, app *deccov1beta2.App) error {
	if app.Spec.Permissions == nil {
		return nil
	}
	// Ensure ServiceAccount exists
	sa := app.Spec.PodSpec.ServiceAccountName
	if sa == "" {
		sa = app.Name
		err = r.Client.Create(ctx, &v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name: sa,
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(app, deccov1beta2.GroupVersion.WithKind("App")),
				},
			},
		})
		if err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
	}
	// Ensure Role exists
	err = r.Client.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: sa,
			Namespace: app.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(app, deccov1beta2.GroupVersion.WithKind("App")),
			},
		},
		Rules: app.Spec.Permissions,
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	// Ensure RoleBinding exists
	err = r.Client.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: saName,
			Namespace: app.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(app, deccov1beta2.GroupVersion.WithKind("App")),
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: app.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     saName,
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}


func prepareApp(app *deccov1beta2.App) error {
	// insertDomainEnvVar into each container
	if app.Spec.DomainEnvVarName == "" {
		return nil
	}
	for i := range app.Spec.PodSpec.Containers {
		containers[i].Env = append(containers[i].Env, v1.EnvVar{
			Name:  ar.app.Spec.DomainEnvVarName,
			Value: ar.spaceSpec.DomainName,
		})
	}
}

func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1beta2.App{}).
		Complete(r)
}
