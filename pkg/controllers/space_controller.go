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
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/decco"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/slack"
	"github.com/platform9/decco/pkg/validate"
)

const (
	defaultHttpInternalPort int32 = 8081
	metricsPort             int32 = 10254
	spaceCreatorRole              = "space-creator"
	httpDefaultName               = "default-http"
	namespace                     = "decco.platform9.com"
	finalizerSpaceDNS             = "dns." + namespace
	finalizerSpaceNamespace       = "namespace." + namespace
	labelSpaceName                = namespace + "/name"
	labelSpaceNamespace           = namespace + "/namespace"
)

// SpaceReconciler reconciles a Space object
type SpaceReconciler struct {
	dns     dns.Provider
	client  ctrlclient.Client
	ingress decco.Ingress
	log     logr.Logger
}

func NewSpaceReconciler(client ctrlclient.Client, dns dns.Provider, ingress decco.Ingress, log logr.Logger) *SpaceReconciler {
	if log == nil {
		log = ctrl.Log
	}
	return &SpaceReconciler{
		client:  client,
		dns:     dns,
		ingress: ingress,
		log:     log.WithName("controllers").WithName("Space"),
	}
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
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//
// RBAC permissions necessary for the ingress role:
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create
// +kubebuilder:rbac:groups="",resources=ingress/status,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch

// TODO(erwin) fix "ready" check, let every reconciler do so themselves
func (r *SpaceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.log.WithValues("space", req.NamespacedName)

	// Lookup the current Space
	space := &deccov1beta2.Space{}
	err := r.client.Get(ctx, req.NamespacedName, space)
	if err != nil {
		return ctrl.Result{}, ctrlclient.IgnoreNotFound(err)
	}

	// Always update the space at the end of the reconciliation loop
	defer func() {
		err := r.client.Update(ctx, space)
		if err != nil {
			log.Error(err, "Failed to update the Space.")
		}
	}()

	if err := validate.SpaceSpec(space.Spec); err != nil {
		return ctrl.Result{}, err
	}

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

	log.Info("Reconciling certificates")
	err = r.reconcileCertificates(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile certificates: %w", err)
	}

	log.Info("Reconciling DNS records")
	err = r.reconcileDNS(ctx, space)
	if err != nil {
		return ctrl.Result{},
			fmt.Errorf("failed to reconcile the DNS records: %w", err)
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

	// If all reconciliation loops succeeded and everything is ready,
	// set the phase to active.
	if space.Status.Phase == deccov1beta2.AppPhaseCreating &&
		space.Status.Namespace != "" &&
		(r.dns == nil || space.Status.Hostname != "") {
		// TODO status check resources inside the space
		log.Info("Updating the space status to Active, because all resources have been successfully reconciled.")
		space.Status.SetPhase(deccov1beta2.SpacePhaseActive, "Space successfully created")
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
		space.Status.SetPhase(deccov1beta2.SpacePhaseCreating, "")
	case isDeleting(space):
		log.Info("Setting the space phase to Deleting, because the space has been scheduled for deletion.")
		space.Status.SetPhase(deccov1beta2.SpacePhaseDeleting, "")
	case space.Status.Phase == deccov1beta2.AppPhaseCreating && space.Status.Namespace != "" && (r.dns == nil || space.Status.Hostname != ""):
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
	err := r.client.Get(ctx, types.NamespacedName{
		Name: namespace,
	}, ns)
	if err == nil {

		// Namespace already exists.
		//
		// Check if we are not reusing an existing space owned by the space.
		if !hasSpaceAsOwnerInLabels(ns.Labels, space) {
			return fmt.Errorf("namespace %s already exists", namespace)
		}

		// Delete the namespace if it not yet scheduled for deletion.
		if isDeleting(space) {
			if ns.DeletionTimestamp.IsZero() {
				log.Info("Deleting the namespace.", "namespace", namespace)
				err := r.client.Delete(ctx, ns)
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
		err = r.client.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
				Labels: map[string]string{
					"app":                "decco",
					"decco-space-rsc-ns": namespace,
					// Hack: add labels to be able to locate the space
					// associated to this namespace.
					labelSpaceName:      space.Name,
					labelSpaceNamespace: space.Namespace,
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

	r.getLoggerFor(space).Info("Creating NetworkPolicy", "name", space.Name, "namespace", space.Status.Namespace)
	err := r.client.Create(ctx, &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      space.Name,
			Namespace: space.Status.Namespace,
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
	r.getLoggerFor(space).Info("Creating Role", "name", spaceCreatorRole, "namespace", space.Status.Namespace)
	err := r.client.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spaceCreatorRole,
			Namespace: space.Status.Namespace,
		},
		Rules: perms.Rules,
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create space-creator role: %s", err)
	}

	// Bind the space-creator role to the target subject defined in the space.
	r.getLoggerFor(space).Info("Creating RoleBinding", "name", spaceCreatorRole, "namespace", space.Status.Namespace)
	err = r.client.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spaceCreatorRole,
			Namespace: space.Status.Namespace,
		},
		Subjects: []rbacv1.Subject{
			perms.Subject,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     spaceCreatorRole,
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
	log.Info("Checking HTTP certificate")
	err := r.reconcileHTTPCertificate(ctx, space)
	if err != nil {
		return err
	}

	// Copy the TCP certificate to the space and delete the original one.
	log.Info("Checking TCP certificate")
	err = r.reconcileTCPCertificate(ctx, space)
	if err != nil {
		return err
	}

	return nil
}

func (r *SpaceReconciler) reconcileHTTPCertificate(ctx context.Context, space *deccov1beta2.Space) error {
	if space.Spec.HttpCertSecretName == "" {
		return nil
	}

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		r.getLoggerFor(space).Info("Skipping HTTP cert reconciler; relying on the namespace reconciler to delete all resources.")
		return nil
	}

	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// Skip if secret is already present in namespace
	existingSecret := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{
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
	namespacedName := types.NamespacedName{
		Name:      space.Spec.HttpCertSecretName,
		Namespace: space.Namespace,
	}
	err = r.client.Get(ctx, namespacedName, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to read http cert: %w", err)
	}

	// Validate the secret
	if err := validate.TLSSecret(httpSecret); err != nil {
		return fmt.Errorf("HttpCertSecret (%s) is invalid: %w", namespacedName, err)
	}

	// Copy the secret to the space's namespace
	httpSecret.ObjectMeta = k8sutil.StripClusterDataFromObjectMeta(httpSecret.ObjectMeta)
	httpSecret.Namespace = space.Status.Namespace
	r.getLoggerFor(space).Info("Copying HTTPCertSecret to Space", "src", namespacedName, "dst", space.Status.Namespace)
	err = r.client.Create(ctx, httpSecret)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to copy http cert: %w", err)
	}

	// Delete the original secret
	if space.Spec.DeleteHttpCertSecretAfterCopy {
		r.getLoggerFor(space).Info("Deleting the HTTP secret because DeleteHttpCertSecretAfterCopy was set")
		err = r.client.Delete(ctx, httpSecret)
		if err != nil {
			return fmt.Errorf("failed to delete http cert: %w", err)
		}
	}
	return nil
}

func (r *SpaceReconciler) reconcileTCPCertificate(ctx context.Context, space *deccov1beta2.Space) error {
	if space.Spec.TcpCertAndCaSecretName == "" {
		return nil
	}

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		r.getLoggerFor(space).Info("Skipping TCP cert reconciler; relying on the namespace reconciler to delete all resources.")
		return nil
	}

	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}

	// Skip if secret is already present in namespace
	existingSecret := &corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{
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
	namespacedName := types.NamespacedName{
		Name:      space.Spec.TcpCertAndCaSecretName,
		Namespace: space.Namespace,
	}
	err = r.client.Get(ctx, namespacedName, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to read TCP cert and CA: %s", err)
	}

	// Copy the secret to the space's namespace
	r.getLoggerFor(space).Info("Copying TcpCertAndCaSecret to Space", "src", namespacedName, "dst", space.Status.Namespace)
	err = r.client.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      httpSecret.Name,
			Namespace: space.Status.Namespace,
		},
		Type:       httpSecret.Type,
		Data:       httpSecret.Data,
		StringData: httpSecret.StringData,
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to copy TCP cert and CA: %w", err)
	}

	// Delete the original secret if requested
	if space.Spec.DeleteTcpCertAndCaSecretAfterCopy {
		r.getLoggerFor(space).Info("Deleting the TCP secret because DeleteTcpCertAndCaSecretAfterCopy was set")
		err = r.client.Delete(ctx, httpSecret)
		if err != nil {
			return fmt.Errorf("failed to delete TCP cert and CA: %w", err)
		}
	}
	return nil
}

func (r *SpaceReconciler) reconcileIngress(ctx context.Context, space *deccov1beta2.Space) error {
	if err := ensureNamespaceIsCreated(space); err != nil {
		return err
	}
	log := r.getLoggerFor(space)

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		log.Info("Skipping ingress reconciler; relying on the namespace reconciler to delete all resources.")
		return nil
	}

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
		httpDefaultName,
		defaultHttpSvcPort,
		"",
		space.Spec.EncryptHttp,
		space.Spec.HttpCertSecretName,
		false,
		nil,
	)
	r.getLoggerFor(space).Info("Creating Ingress", "name", ingress.Name, "namespace", ingress.Namespace)
	err := r.client.Create(ctx, ingress)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create http-ingress: %w", err)
	}

	// Create the default-http deployment
	var volumes []corev1.Volume
	containers := []corev1.Container{
		{
			Name:  httpDefaultName,
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
	r.getLoggerFor(space).Info("Creating Deployment", "name", httpDefaultName, "namespace", space.Status.Namespace)
	err = r.client.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      httpDefaultName,
			Namespace: space.Status.Namespace,
			Labels: map[string]string{
				"app": "decco",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: nil,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": httpDefaultName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: httpDefaultName,
					Labels: map[string]string{
						"app": httpDefaultName,
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
	r.getLoggerFor(space).Info("Creating Service", "name", httpDefaultName, "namespace", space.Status.Namespace)
	err = r.client.Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      httpDefaultName,
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
				"app": httpDefaultName,
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
	log := r.getLoggerFor(space)

	// hack: rely on the namespace deletion to clean up these resources.
	if isDeleting(space) {
		log.Info("Skipping private ingress controller reconciler; relying on the namespace reconciler to delete all resources.")
		return nil
	}

	if space.Spec.DisablePrivateIngressController {
		log.Info("Skipping reconciling of private ingress controller; it has" +
			" been disabled.")
		return nil
	}

	// Create the ingress service account.
	log.Info("Creating ServiceAccount", "name", "nginx-ingress", "namespace", space.Status.Namespace)
	err := r.client.Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Status.Namespace,
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx-ingress svc acct: %w", err)
	}

	// Create the ingress RBAC role.
	log.Info("Creating Role", "name", "ingress-controller", "namespace", space.Status.Namespace)
	err = r.client.Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-controller",
			Namespace: space.Status.Namespace,
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
	log.Info("Creating RoleBinding", "name", "nginx-ingress", "namespace", space.Status.Namespace)
	err = r.client.Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Status.Namespace,
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

	// TODO(erwin) replace nginx-ingress APP with a deployment (make completely Apps optional from Decco's pov)
	log.Info("Creating App", "name", "nginx-ingress", "namespace", space.Status.Namespace)
	replicas := int32(1)
	err = r.client.Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Status.Namespace,
			Labels: map[string]string{
				// TODO(erwin) kept for backward-compatibility, remove if possible
				"decco-derived-from": "app",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			// FirstEndpointListenPort: baseTlsListenPort + 1, // 'cause nginx itself uses 443
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					// TODO(erwin) kept for backward-compatibility, remove if possible
					Annotations: map[string]string{
						"linkerd.io/inject": "enabled",
						// https://linkerd.io/2/features/protocol-detection/#configuring-protocol-detection
						// Skip linkerd proxy when making outbound connections to mysql
						"config.linkerd.io/skip-outbound-ports": "3306",
					},
					Labels: map[string]string{
						"app":       "decco",
						"decco-app": "nginx-ingress",
					},
				},
				Spec: corev1.PodSpec{
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
			},
		},
		Status: appsv1.DeploymentStatus{},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx-ingress app: %w", err)
	}
	return nil
}

func (r *SpaceReconciler) reconcileDNS(ctx context.Context, space *deccov1beta2.Space) error {
	log := r.getLoggerFor(space)
	if r.dns == nil {
		log.Info("skipping DNS update: no registered provider")
		return nil
	}

	ipOrHostname, err := r.ingress.GetIPOrHostname(ctx)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress ipOrHostname: %s", err)
	}
	url := os.Getenv("SLACK_WEBHOOK_FOR_DNS_UPDATE_FAILURE")

	if isDeleting(space) {
		// Skip deleting DNS if it is not configured
		// TODO(erwin) replace with an actual check if it has been configured
		if space.Status.Hostname == "" {
			return nil
		}

		err = r.dns.DeleteRecord(space.Spec.DomainName, space.Name, ipOrHostname)
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
		if space.Status.Hostname != "" {
			return nil
		}

		err = r.dns.CreateOrUpdateRecord(space.Spec.DomainName, space.Name, ipOrHostname)
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
		space.Status.Hostname = fmt.Sprintf("%s.%s", space.Name, space.Spec.DomainName)
	}
	return err
}

func (r *SpaceReconciler) getLoggerFor(space *deccov1beta2.Space) logr.Logger {
	return r.log.WithValues("space", types.NamespacedName{
		Namespace: space.Namespace,
		Name:      space.Name,
	})
}

func ensureNamespaceIsCreated(space *deccov1beta2.Space) error {
	if space.Status.Namespace == "" {
		return fmt.Errorf("the space's namespace has not yet been created")
	}
	return nil
}

func hasSpaceAsOwnerInLabels(labels map[string]string, space *deccov1beta2.Space) bool {
	return labels[labelSpaceName] == space.Name && labels[labelSpaceNamespace] == space.Namespace
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
