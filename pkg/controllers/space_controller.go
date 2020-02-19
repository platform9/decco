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

	"github.com/cenkalti/backoff"
	"github.com/go-logr/logr"
	"k8s.io/api/apps/v1beta1"
	v1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deccov1 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/misc"
	"github.com/platform9/decco/pkg/slack"
)

const (
	defaultHttpInternalPort int32 = 8081
	metricsPort             int32 = 10254
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
		// TODO(erwin) delete DNS record
		return ctrl.Result{}, nil
	}

	// Validate object
	// TODO(erwin) Validate secrets too
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
			fmt.Errorf("failed to reconcile DNS records: %w", err)
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
	// This probably should be part of the overall validation?
	if space.Spec.Project == "" {
		r.getLoggerFor(space).Info("Skipping network policy " +
			"because no project was provided in the space.")
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
		r.getLoggerFor(space).Info("Skipping RBAC rules, " +
			"because no permissions were defined in the space.")
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

func (r *SpaceReconciler) reconcileCertificates(ctx context.Context, space *deccov1.Space) error {
	log := r.getLoggerFor(space)

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

func (r *SpaceReconciler) reconcileHTTPCertificate(ctx context.Context, space *deccov1.Space) error {
	// Skip if secret is already present in namespace
	existingSecret := &v1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: space.Name,
		Name:      space.Spec.TcpCertAndCaSecretName,
	}, existingSecret)
	if err == nil {
		r.getLoggerFor(space).Info("Skipping HTTP certificate: secret is already present at target" +
			" location.")
		return nil
	}

	// Fetch the expected secret
	httpSecret := &v1.Secret{}
	err = r.Client.Get(ctx, types.NamespacedName{
		Namespace: space.Namespace,
		Name:      space.Spec.HttpCertSecretName,
	}, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to read http cert: %w", err)
	}

	// Copy the secret to the space's namespace
	err = r.Client.Create(ctx, &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: space.Name,
			Name:      httpSecret.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
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

func (r *SpaceReconciler) reconcileTCPCertificate(ctx context.Context, space *deccov1.Space) error {
	if space.Spec.TcpCertAndCaSecretName == "" {
		return nil
	}

	// Skip if secret is already present in namespace
	existingSecret := &v1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Namespace: space.Name,
		Name:      space.Spec.TcpCertAndCaSecretName,
	}, existingSecret)
	if err == nil {
		r.getLoggerFor(space).Info("Skipping TCP certificate: secret is already present at target" +
			" location.")
		return nil
	}

	// Fetch the expected secret
	httpSecret := &v1.Secret{}
	err = r.Client.Get(ctx, types.NamespacedName{
		Namespace: space.Namespace,
		Name:      space.Spec.TcpCertAndCaSecretName,
	}, httpSecret)
	if err != nil {
		return fmt.Errorf("failed to read TCP cert and CA: %s", err)
	}

	// Copy the secret to the space's namespace
	err = r.Client.Create(ctx, &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: space.Name,
			Name:      httpSecret.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
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

func (r *SpaceReconciler) reconcileDNS(ctx context.Context, space *deccov1.Space) error {
	// TODO check current states
	deleteRecord := false // TODO support DNS deletion
	if !dns.Enabled() {
		r.getLoggerFor(space).Info("skipping DNS update: no registered provider")
		return nil
	}

	ipOrHostname, isHostname, err := k8sutil.GetTcpIngressIPOrHostnameWithControllerClient(ctx, r.Client)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress ipOrHostname: %s", err)
	}
	expBackoff := misc.DefaultBackoff()
	url := os.Getenv("SLACK_WEBHOOK_FOR_DNS_UPDATE_FAILURE")
	attempt := 0
	verb := "create"
	if deleteRecord {
		verb = "delete"
	}
	log := r.getLoggerFor(space)
	updateFn := func() error {
		var err error
		attempt += 1
		if deleteRecord {
			err = dns.DeleteRecord(space.Spec.DomainName, space.Name, ipOrHostname, isHostname)
		} else {
			err = dns.CreateOrUpdateRecord(space.Spec.DomainName, space.Name, ipOrHostname, isHostname)
		}
		if err != nil {
			msg := fmt.Sprintf("attempt %d to %s DNS for %s failed: %s",
				attempt, verb, space.Name, err)
			log.Info("Failed to update record: " + msg)
			err = fmt.Errorf("%s", msg)
			if url != "" {
				slack.PostBestEffort(url, msg, log)
			}
		} else if url != "" && attempt >= 2 {
			msg := fmt.Sprintf("DNS %s for %s succeeded after %d attempts",
				verb, space.Name, attempt)
			slack.PostBestEffort(url, msg, log)
		}
		return err
	}
	return backoff.RetryNotify(updateFn, expBackoff, nil)
}

func (r *SpaceReconciler) reconcileIngress(ctx context.Context, space *deccov1.Space) error {
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
		space.Name,
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
	ingress.OwnerReferences = append(ingress.OwnerReferences, *metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")))
	err := r.Client.Create(ctx, ingress)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create http-ingress: %w", err)
	}

	// Create the default-http deployment
	var volumes []v1.Volume
	containers := []v1.Container{
		{
			Name:  "default-http",
			Image: "platform9systems/decco-default-http",
			Env: []v1.EnvVar{
				{
					Name: "MY_POD_NAMESPACE",
					ValueFrom: &v1.EnvVarSource{
						FieldRef: &v1.ObjectFieldSelector{
							FieldPath: "metadata.namespace",
						},
					},
				},
			},
			LivenessProbe: &v1.Probe{
				Handler: v1.Handler{
					HTTPGet: &v1.HTTPGetAction{
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
			Resources: v1.ResourceRequirements{
				Limits: v1.ResourceList{
					"cpu": resource.Quantity{
						Format: "10m",
					},
					"memory": resource.Quantity{
						Format: "20Mi",
					},
				},
				Requests: v1.ResourceList{
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
		containers[0].Ports = []v1.ContainerPort{
			{ContainerPort: defaultHttpInternalPort},
		}
	}
	err = r.Client.Create(ctx, &v1beta1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-http",
			Labels: map[string]string{
				"app": "decco",
			},
		},
		Spec: v1beta1.DeploymentSpec{
			Replicas: nil,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "default-http",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default-http",
					Labels: map[string]string{
						"app": "default-http",
					},
				},
				Spec: v1.PodSpec{
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
	err = r.Client.Create(ctx, &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-http",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
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

func (r *SpaceReconciler) reconcilePrivateIngressController(ctx context.Context, space *deccov1.Space) error {
	log := r.getLoggerFor(space)
	if space.Spec.DisablePrivateIngressController {
		log.Info("Skipping reconciling of private ingress controller; it has" +
			" been disabled.")
		return nil
	}

	// Create the ingress service account.
	err := r.Client.Create(ctx, &v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
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
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
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
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
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
	endpoints := []deccov1.EndpointSpec{
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
		endpoints = append(endpoints, deccov1.EndpointSpec{
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

	err = r.Client.Create(ctx, &deccov1.App{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-ingress",
			Namespace: space.Name,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(space, deccov1.GroupVersion.WithKind("Space")),
			},
		},
		Spec: deccov1.AppSpec{
			InitialReplicas:         1,
			FirstEndpointListenPort: baseTlsListenPort + 1, // 'cause nginx itself uses 443
			Permissions:             []rbacv1.PolicyRule{},
			PodSpec: v1.PodSpec{
				ServiceAccountName: "nginx-ingress",
				Containers: []v1.Container{
					{
						Name: "nginx-ingress",
						Args: args,
						Env: []v1.EnvVar{
							{
								Name: "POD_NAME",
								ValueFrom: &v1.EnvVarSource{
									FieldRef: &v1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.name",
									},
								},
							},
							{
								Name: "POD_NAMESPACE",
								ValueFrom: &v1.EnvVarSource{
									FieldRef: &v1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.namespace",
									},
								},
							},
						},
						Image: ingressControllerImage,
						Ports: []v1.ContainerPort{
							{
								ContainerPort: int32(443),
							},
							{
								Name:          "metrics",
								ContainerPort: metricsPort,
							},
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								"cpu":    resource.MustParse("100m"),
								"memory": resource.MustParse("200Mi"),
							},
							Limits: v1.ResourceList{
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

func (r *SpaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1.Space{}).
		Complete(r)
}

func (r *SpaceReconciler) getLoggerFor(space *deccov1.Space) logr.Logger {
	return r.Log.WithValues("space", types.NamespacedName{
		Namespace: space.Namespace,
		Name:      space.Name,
	})
}
