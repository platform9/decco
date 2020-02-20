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
	"os"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/slack"
)

const (
	defaultHttpInternalPort int32  = 8081
	metricsPort             int32  = 10254
	finalizerSpaceDNS       string = "dns.space.decco.platform9.com"
	finalizerSpaceNamespace string = "namespace.space.decco.platform9.com"
)

// SpaceReconciler reconciles a Space object
type SpaceReconciler struct {
	client.Client
	Log logr.Logger
}

// TODO(erwin) Narrow down RBAC rules
// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=decco.platform9.com,resources=apps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete

func (r *SpaceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("space", req.NamespacedName)

	// Lookup the current Space
	space := &deccov1beta2.Space{}
	err := r.Client.Get(ctx, req.NamespacedName, space)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Always update the space at the end of the reconcilation loop
	defer func() {
		err := r.Client.Update(ctx, space)
		if err != nil {
			log.Error(err, "Failed to update the Space.")
		}
	}()

	// Update the phase of the Space.
	r.updatePhase(space)

	// Note: for now we do not reconcile during the active phase.
	// TODO(erwin) handle updates to space resource (in Active phase).
	if space.Status.Phase == deccov1beta2.SpacePhaseActive {
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling namespace")
	err = r.reconcileNamespace(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile the namespace: %w", err)
	}

	log.Info("Reconciling RBAC rules")
	err = r.reconcileRBAC(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile RBAC rules: %w", err)
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

	log.Info("Reconciling certificates")
	err = r.reconcileCertificates(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile certificates: %w", err)
	}

	log.Info("Reconciling default HTTP deployment and ingress")
	err = r.reconcileIngress(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile default HTTP deployment and ingress: %w", err)
	}

	log.Info("Reconciling private ingress controller")
	err = r.reconcilePrivateIngressController(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile private ingress controller: %w", err)
	}

	log.Info("Reconciling DNS records")
	err = r.reconcileDNS(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile the DNS records: %w", err)
	}

	// Hack: while actively deleting we might need to wait a bit for
	// other resources to be deleted. To avoid having to wait for the next
	// periodic update, we forcefully requeue the space every couple of seconds.
	if isDeleting(space) {
		requeueAfter := 10 * time.Second
		log.Info("Requeueing space because it is being deleted.", "requeueAfter", requeueAfter)
		return ctrl.Result{
			RequeueAfter: requeueAfter,
		}, nil
	}

	return ctrl.Result{}, nil
}

// TODO(erwin) add controller ownerReference and add .Owns here
func (r *SpaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1beta2.Space{}).
		Complete(r)
}

func (r *SpaceReconciler) updatePhase(space *deccov1beta2.Space) {
	log := r.getLoggerFor(space)
	switch {
	case space.Status.Phase == "":
		log.Info("Setting the space phase to Creating, because the space has no phase set.")
		space.Status.Phase = deccov1beta2.SpacePhaseCreating
	case isDeleting(space):
		log.Info("Setting the space phase to Deleting, because the space has been scheduled for deletion.")
		space.Status.Phase = deccov1beta2.SpacePhaseDeleting
	case space.Status.Phase == deccov1beta2.AppPhaseCreating && space.Status.Namespace != "" && (!dns.Enabled() || space.Status.DNSConfigured):
		// TODO status check resources inside the space
		log.Info("Updating the space status to Active, because all resources have been successfully reconciled.")
		space.Status.SetPhase(deccov1beta2.SpacePhaseActive, "Space successfully created")
	}
}

func (r *SpaceReconciler) reconcileNamespace(ctx context.Context, space *deccov1beta2.Space) error {
	log := r.getLoggerFor(space)
	namespace := space.Name

	// Check what the current status is of the namespace.
	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name: namespace,
	}, ns)
	if err == nil {

		// Namespace already exists.
		//
		// Check if we are not reusing an existing space not owned by the space.
		if !hasSpaceAsOwner(ns.OwnerReferences, space) {
			return fmt.Errorf("namespace %s already exists", namespace)
		}

		// Delete the namespace if it not yet scheduled for deletion.
		if isDeleting(space) {
			if ns.DeletionTimestamp.IsZero() {
				log.Info("Deleting the namespace.", "namespace", namespace)
				err := r.Client.Delete(ctx, ns)
				if err != nil {
					return err
				}
			} else {
				log.Info("Awaiting deletion of the namespace.", "namespace", namespace)
			}
		}
	} else {
		// Stop if there is an unexpected error.
		if !errors.IsNotFound(err) {
			return err
		}

		// Skip if the space is being deleted.
		if isDeleting(space) {
			// Remove the finalizer, because the space is deleting and the
			// namespace is no longer there.
			log.Info("Removing finalizer.", "finalizer", finalizerSpaceNamespace)
			removeFinalizer(space, finalizerSpaceNamespace)
			return nil
		}

		// Set the finalizer to ensure that we get time to delete the ns.
		//
		// Currently this is done by the OwnerReference. However, this is based
		// on undefined behaviour, since a cluster-scoped resource (namespace)
		// should not be able to have a namespace-scoped resource (space) as its
		// owner. It currently works, but we should not trust it to be the case
		// in the future.
		log.Info("Adding finalizer.", "finalizer", finalizerSpaceNamespace)
		addFinalizer(space, finalizerSpaceNamespace)

		// Attempt to create the namespace.
		log.Info("Creating the namespace for the space.", "namespace", namespace)
		err = r.Client.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
				OwnerReferences: []metav1.OwnerReference{
					createSpaceOwnerRef(space),
				},
				Labels: map[string]string{
					"app":                "decco",
					"decco-space-rsc-ns": namespace,
					// Hack: add labels to be able to locate the space
					// associated to this namespace.
					"space.decco.platform9.com/name":      space.Name,
					"space.decco.platform9.com/namespace": space.Namespace,
				},
			},
		})
		if err != nil {
			return err
		}
	}

	// Ensure that the namespace is set in the status
	space.Status.Namespace = namespace
	return nil
}

func (r *SpaceReconciler) reconcileNetPolicy(ctx context.Context, space *deccov1beta2.Space) error {
	// note(erwin): from original code, not sure why this condition is here.
	// This probably should be part of the overall validation?
	if space.Spec.Project == "" {
		r.getLoggerFor(space).Info("Skipping network policy " +
			"because no project was provided in the space.")
		return nil
	}

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		return nil
	}

	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	err := r.Client.Create(ctx, &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      space.Name,
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1beta2.GroupVersion.WithKind("Space")),
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
									"decco-project": deccov1beta2.ReservedProjectName,
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

func (r *SpaceReconciler) reconcileRBAC(ctx context.Context, space *deccov1beta2.Space) error {
	log := r.getLoggerFor(space)
	perms := space.Spec.Permissions
	if perms == nil {
		log.Info("Skipping RBAC rules, because no permissions were defined in the space.")
		return nil
	}

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		return nil
	}

	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// Ensure that the "space-creator" role is created.
	err := r.Client.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "space-creator",
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
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
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
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

func (r *SpaceReconciler) reconcileCertificates(ctx context.Context, space *deccov1beta2.Space) error {
	log := r.getLoggerFor(space)

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		return nil
	}

	// Copy the HTTP certificate to the space and delete the original one.
	log.Info("Reconciling HTTP certificate")
	err := r.reconcileHTTPCertificate(ctx, space)
	if err != nil {
		return err
	}

	// Copy the TCP certificate to the space and delete the original one.
	log.Info("Reconciling TCP certificate")
	err = r.reconcileTCPCertificate(ctx, space)
	if err != nil {
		return err
	}

	return nil
}

func (r *SpaceReconciler) reconcileHTTPCertificate(ctx context.Context, space *deccov1beta2.Space) error {
	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// Skip if secret is already present in namespace
	existingSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      space.Spec.TcpCertAndCaSecretName,
		Namespace: space.Status.Namespace,
	}, existingSecret)
	if err == nil {
		r.getLoggerFor(space).Info("Skipping HTTP certificate: secret is already present at target" +
			" location.")
		return nil
	}

	// Fetch the expected secret
	httpSecret := &corev1.Secret{}
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      space.Spec.HttpCertSecretName,
		Namespace: space.Namespace,
	}, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to read http cert: %w", err)
	}

	// Copy the secret to the space's namespace
	err = r.Client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      httpSecret.Name,
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
			},
		},
		Data:       httpSecret.Data,
		StringData: httpSecret.StringData,
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to copy http cert: %w", err)
	}

	// Delete the original secret
	err = r.Client.Delete(ctx, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to delete http cert: %w", err)
	}
	return nil
}

func (r *SpaceReconciler) reconcileTCPCertificate(ctx context.Context, space *deccov1beta2.Space) error {
	if space.Spec.TcpCertAndCaSecretName == "" {
		return nil
	}

	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// Skip if secret is already present in namespace
	existingSecret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      space.Spec.TcpCertAndCaSecretName,
		Namespace: space.Status.Namespace,
	}, existingSecret)
	if err == nil {
		r.getLoggerFor(space).Info("Skipping TCP certificate: secret is already present at target" +
			" location.")
		return nil
	}

	// Fetch the expected secret
	httpSecret := &corev1.Secret{}
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      space.Spec.TcpCertAndCaSecretName,
		Namespace: space.Namespace,
	}, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to read TCP cert and CA: %s", err)
	}

	// Copy the secret to the space's namespace
	err = r.Client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      httpSecret.Name,
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
			},
		},
		Data:       httpSecret.Data,
		StringData: httpSecret.StringData,
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to copy TCP cert and CA: %w", err)
	}

	// Delete the original secret
	err = r.Client.Delete(ctx, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to delete TCP cert and CA: %w", err)
	}
	return nil
}

func (r *SpaceReconciler) reconcileIngress(ctx context.Context, space *deccov1beta2.Space) error {
	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		return nil
	}

	log := r.getLoggerFor(space)
	if !space.Spec.CreateDefaultHttpDeploymentAndIngress {
		log.Info("Skipping reconciling HTTP ingress, because it is disabled.")
		return nil
	}

	hostName := space.Name + "." + space.Spec.DomainName
	defaultHttpSvcPort := int32(80)
	if space.Spec.EncryptHttp {
		defaultHttpSvcPort = k8sutil.TlsPort
	}

	// Create the HTTP ingress
	ingress := k8sutil.NewHTTPIngress(
		space.Status.Namespace,
		"http-ingress",
		map[string]string{"app": "decco"},
		hostName,
		"/",
		"default-http",
		defaultHttpSvcPort,
		"",
		space.Spec.EncryptHttp,
		space.Spec.HttpCertSecretName,
		false,
		nil,
	)
	ingress.OwnerReferences = append(ingress.OwnerReferences, createSpaceOwnerRef(space))
	err := r.Client.Create(ctx, ingress)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create http-ingress: %w", err)
	}

	// Create the default-http deployment
	var volumes []corev1.Volume
	containers := []corev1.Container{
		{
			Name:  "default-http",
			Image: "platform9systems/decco-default-http",
			Env: []corev1.EnvVar{
				{
					Name: "MY_POD_NAMESPACE",
					ValueFrom: &corev1.EnvVarSource{
						FieldRef: &corev1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
			},
			LivenessProbe: &corev1.Probe{
				Handler: corev1.Handler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/healthz",
						Port: intstr.IntOrString{
							Type:   intstr.Int,
							IntVal: defaultHttpInternalPort,
						},
						Scheme: "HTTP",
					},
				},
				InitialDelaySeconds: 30,
				TimeoutSeconds:      5,
			},
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					"cpu": resource.Quantity{
						Format: "10m",
					},
					"memory": resource.Quantity{
						Format: "20Mi",
					},
				},
				Requests: corev1.ResourceList{
					"cpu": resource.Quantity{
						Format: "10m",
					},
					"memory": resource.Quantity{
						Format: "20Mi",
					},
				},
			},
		},
	}
	if space.Spec.EncryptHttp {
		destHostAndPort := fmt.Sprintf("%d", defaultHttpInternalPort)
		volumes, containers = k8sutil.InsertStunnel("stunnel",
			k8sutil.TlsPort, "no",
			destHostAndPort, "", space.Spec.HttpCertSecretName,
			true, false, volumes,
			containers, 0, 0)
	} else {
		containers[0].Ports = []corev1.ContainerPort{
			{ContainerPort: defaultHttpInternalPort},
		}
	}
	err = r.Client.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-http",
			Namespace: space.Status.Namespace,
			Labels: map[string]string{
				"app": "decco",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: nil,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "default-http",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default-http",
					Labels: map[string]string{
						"app": "default-http",
					},
				},
				Spec: corev1.PodSpec{
					Containers: containers,
					Volumes:    volumes,
				},
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create default-http deployment: %w", err)
	}

	// Create the default-http service
	svcPort := int32(80)
	targetPort := defaultHttpInternalPort
	if space.Spec.EncryptHttp {
		svcPort = k8sutil.TlsPort
		targetPort = k8sutil.TlsPort
	}
	err = r.Client.Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-http",
			Namespace: space.Status.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port: svcPort,
					TargetPort: intstr.IntOrString{
						Type:   intstr.Int,
						IntVal: targetPort,
					},
				},
			},
			Selector: map[string]string{
				"app": "default-http",
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create default-http service: %w", err)
	}
	return nil
}

func (r *SpaceReconciler) reconcilePrivateIngressController(ctx context.Context, space *deccov1beta2.Space) error {
	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		return nil
	}

	log := r.getLoggerFor(space)
	if space.Spec.DisablePrivateIngressController {
		log.Info("Skipping reconciling of private ingress controller; it has" +
			" been disabled.")
		return nil
	}

	// Create the ingress service account.
	err := r.Client.Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx-ingress svc acct: %w", err)
	}

	// Create the ingress RBAC role.
	err = r.Client.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-controller",
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"*"},
				Resources: []string{"events"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"*"},
				Resources: []string{"configmaps"},
				Verbs:     []string{"create", "update", "get", "watch", "list"},
			},
			{
				APIGroups: []string{"*"},
				Resources: []string{"ingresses", "ingresses/status"},
				Verbs:     []string{"update", "get", "watch", "list"},
			},
			{
				APIGroups: []string{"*"},
				Resources: []string{"pods", "services", "secrets",
					"namespaces", "endpoints"},
				Verbs: []string{"get", "watch", "list"},
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx-controller role: %w", err)
	}

	// Bind the ingress role to the service account
	err = r.Client.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
			},
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "ServiceAccount",
				Name: "nginx-ingress",
			},
		},
		RoleRef: rbacv1.RoleRef{
			Kind: "Role",
			Name: "ingress-controller",
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx-ingress rolebinding: %w", err)
	}

	hostName := space.Name + "." + space.Spec.DomainName
	args := []string{
		"/nginx-ingress-controller",
		"--default-backend-service=$(POD_NAMESPACE)/default-http",
		"--http-only-ipv4-bindaddress-prefix=127.0.0.1:",
		"--enable-ssl-chain-completion=false",
		fmt.Sprintf("--watch-namespace=%s", space.Name),
	}
	if space.Spec.VerboseIngressControllerLogging || os.Getenv("VERBOSE_INGRESS_CONTROLLER_LOGGING") != "" {
		args = append(args, "--v=5")
	} else {
		args = append(args, "--v=1")
	}
	baseTlsListenPort := int32(k8sutil.TlsPort)
	endpoints := []deccov1beta2.EndpointSpec{
		{
			Name:                  "nginx-ingress",
			Port:                  baseTlsListenPort, // nginx itself terminates TLS
			DisableTlsTermination: true,              // no stunnel side-car
			SniHostname:           hostName,
		},
		{
			Name: "nginx-ingress-metrics",
			Port: metricsPort,
		},
	}
	for _, epName := range space.Spec.PrivateIngressControllerTcpEndpoints {
		endpoints = append(endpoints, deccov1beta2.EndpointSpec{
			Name: "nginx-ingress-sni-" + epName,
			Port: 80,
			SniHostname: epName +
				space.Spec.PrivateIngressControllerTcpHostnameSuffix +
				"." + hostName,
		})
	}
	ingressControllerImage := os.Getenv("INGRESS_CONTROLLER_IMAGE")
	if ingressControllerImage == "" {
		// default to a manually built image that has a fix for CORE-843
		ingressControllerImage = "platform9/ingress-nginx:0.19.0-006"
	}

	err = r.Client.Create(ctx, &deccov1beta2.App{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Status.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				createSpaceOwnerRef(space),
			},
		},
		Spec: deccov1beta2.AppSpec{
			InitialReplicas:         1,
			FirstEndpointListenPort: baseTlsListenPort + 1, // 'cause nginx itself uses 443
			Permissions:             []rbacv1.PolicyRule{},
			PodSpec: corev1.PodSpec{
				ServiceAccountName: "nginx-ingress",
				Containers: []corev1.Container{
					{
						Name: "nginx-ingress",
						Args: args,
						Env: []corev1.EnvVar{
							{
								Name: "POD_NAME",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.name",
									},
								},
							},
							{
								Name: "POD_NAMESPACE",
								ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.namespace",
									},
								},
							},
						},
						Image: ingressControllerImage,
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: int32(443),
							},
							{
								Name:          "metrics",
								ContainerPort: metricsPort,
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								"cpu":    resource.MustParse("100m"),
								"memory": resource.MustParse("200Mi"),
							},
							Limits: corev1.ResourceList{
								"cpu":    resource.MustParse("1000m"),
								"memory": resource.MustParse("200Mi"),
							},
						},
					},
				},
			},
			Endpoints: endpoints,
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx-ingress app: %w", err)
	}
	return nil
}

func (r *SpaceReconciler) reconcileDNS(ctx context.Context, space *deccov1beta2.Space) error {
	log := r.getLoggerFor(space)
	if !dns.Enabled() {
		log.Info("skipping DNS update: no registered provider")
		return nil
	}

	ipOrHostname, isHostname, err := k8sutil.GetTcpIngressIPOrHostnameWithControllerClient(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress ipOrHostname: %s", err)
	}
	url := os.Getenv("SLACK_WEBHOOK_FOR_DNS_UPDATE_FAILURE")

	if isDeleting(space) {
		// Skip deleting DNS if it is not configured
		// TODO(erwin) replace with an actual check if it has been configured
		if !space.Status.DNSConfigured {
			return nil
		}

		err = dns.DeleteRecord(space.Spec.DomainName, space.Name, ipOrHostname, isHostname)
		if err != nil {
			if url != "" {
				msg := fmt.Sprintf("attempt to delete DNS for %s failed: %s", space.Name, err)
				slack.PostBestEffort(url, msg, log)
			}
			return fmt.Errorf("failed to delete DNS record: %w", err)
		}
		if url != "" {
			msg := fmt.Sprintf("Deleting DNS records for %s has succeeded!", space.Name)
			slack.PostBestEffort(url, msg, log)
		}

		removeFinalizer(space, finalizerSpaceDNS)
	} else {
		// Check if the DNS has already been configured
		// TODO(erwin) replace with an actual check if it is configured
		if space.Status.DNSConfigured {
			return nil
		}

		err = dns.CreateOrUpdateRecord(space.Spec.DomainName, space.Name, ipOrHostname, isHostname)
		if err != nil {
			if url != "" {
				msg := fmt.Sprintf("attempt to create DNS for %s failed: %s", space.Name, err)
				slack.PostBestEffort(url, msg, log)
			}
			return fmt.Errorf("failed to update DNS record: %w", err)
		}
		if url != "" {
			msg := fmt.Sprintf("Updating DNS records for %s has succeeded!", space.Name)
			slack.PostBestEffort(url, msg, log)
		}

		addFinalizer(space, finalizerSpaceNamespace)
		space.Status.DNSConfigured = true
	}
	return err
}

func (r *SpaceReconciler) getLoggerFor(space *deccov1beta2.Space) logr.Logger {
	return r.Log.WithValues("space", types.NamespacedName{
		Namespace: space.Namespace,
		Name:      space.Name,
	})
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

func ensureNamespaceIsCreated(space *deccov1beta2.Space) error {
	if space.Status.Namespace == "" {
		return fmt.Errorf("the space's namespace has not yet been created")
	}
	return nil
}

func hasSpaceAsOwner(owners []metav1.OwnerReference, space *deccov1beta2.Space) bool {
	needle := createSpaceOwnerRef(space)
	for _, owner := range owners {
		if reflect.DeepEqual(owner, needle) {
			return true
		}
	}
	return false
}

func createSpaceOwnerRef(space *deccov1beta2.Space) metav1.OwnerReference {
	return *metav1.NewControllerRef(space, deccov1beta2.GroupVersion.WithKind("Space"))
}

func isDeleting(space *deccov1beta2.Space) bool {
	return !space.ObjectMeta.DeletionTimestamp.IsZero()
}

func addFinalizer(space *deccov1beta2.Space, finalizer string) {
	if !containsString(space.Finalizers, finalizer) {
		space.Finalizers = append(space.Finalizers, finalizer)
	}
}

func removeFinalizer(space *deccov1beta2.Space, finalizer string) {
	if containsString(space.Finalizers, finalizer) {
		space.Finalizers = removeString(space.Finalizers, finalizer)
	}
}
