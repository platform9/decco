package space

import (
	"github.com/sirupsen/logrus"
	"github.com/platform9/decco/pkg/spec"
	"github.com/platform9/decco/pkg/k8sutil"
	"github.com/platform9/decco/pkg/dns"
	"github.com/platform9/decco/pkg/appspec"
	"github.com/platform9/decco/pkg/slack"
	"github.com/platform9/decco/pkg/misc"
	"reflect"
	"k8s.io/client-go/kubernetes"
	"k8s.io/api/core/v1"
	"k8s.io/api/extensions/v1beta1"
	"fmt"
	"errors"
	"strings"
	"encoding/json"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	rbacv1 "k8s.io/api/rbac/v1"
	"github.com/cenkalti/backoff"
	"os"
)

var (
	errInCreatingPhase = errors.New("space already in Creating phase")
)

const (
	defaultHttpInternalPort int32 = 8081
)

type SpaceRuntime struct {
	kubeApi kubernetes.Interface
	namespace string
	log *logrus.Entry

	//config Config

	Space spec.Space

	// in memory state of the spaceRsc
	// Status is the source of truth after SpaceRuntime struct is materialized.
	Status spec.SpaceStatus
}

// -----------------------------------------------------------------------------

func New(
	spc spec.Space,
	kubeApi kubernetes.Interface,
	namespace string,
) *SpaceRuntime {

	lg := logrus.WithField("pkg","space",
		).WithField("space-name", spc.Name)

	c := &SpaceRuntime{
		kubeApi:   kubeApi,
		log:       lg,
		Space:     spc,
		Status:    spc.Status.Copy(),
		namespace: namespace,
	}

	if err := c.setup(); err != nil {
		c.log.Errorf("cluster failed to setup: %v", err)
		if c.Status.Phase != spec.SpacePhaseFailed {
			c.Status.SetReason(err.Error())
			c.Status.SetPhase(spec.SpacePhaseFailed)
			if err := c.updateCRStatus(); err != nil {
				c.log.Errorf("failed to update space phase (%v): %v",
					spec.SpacePhaseFailed, err)
			}
		}
	}
	return c
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) Update(spc spec.Space) {
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) Delete() (nsDeleted bool) {
	nsApi := c.kubeApi.CoreV1().Namespaces()
	err := nsApi.Delete(c.Space.Name, nil)
	if err != nil {
		c.log.Warnf("failed to delete namespace %s: ", err.Error())
	}
	nsDeleted = err == nil
	err = c.updateDns(true)
	if err != nil {
		c.log.Warnf("failed to delete dns record: %s", err)
	}
	return
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) updateCRStatus() error {
	if reflect.DeepEqual(c.Space.Status, c.Status) {
		return nil
	}

	newCrg := c.Space
	newCrg.Status = c.Status
	newCrg, err := k8sutil.UpdateSpaceCustRsc(
		c.kubeApi.CoreV1().RESTClient(),
		c.namespace,
		newCrg)
	if err != nil {
		return fmt.Errorf("failed to update Space Status: %v", err)
	}

	c.Space = newCrg
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) setup() error {
	err := c.Space.Spec.Validate()
	if err != nil {
		return err
	}

	var shouldCreateResources bool
	switch c.Status.Phase {
	case spec.SpacePhaseNone:
		shouldCreateResources = true
	case spec.SpacePhaseCreating:
		return errInCreatingPhase
	case spec.SpacePhaseActive:
		shouldCreateResources = false

	default:
		return fmt.Errorf("unexpected space phase: %s", c.Status.Phase)
	}

	if shouldCreateResources {
		return c.create()
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) phaseUpdateError(op string, err error) error {
	return fmt.Errorf(
		"%s : failed to update space phase (%v): %v",
		op,
		c.Status.Phase,
		err,
	)
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) create() error {
	c.Status.SetPhase(spec.SpacePhaseCreating)
	if err := c.updateCRStatus(); err != nil {
		return c.phaseUpdateError("space create", err)
	}
	if err := c.internalCreate(); err != nil {
		return err
	}
	c.Status.SetPhase(spec.SpacePhaseActive)
	if err := c.updateCRStatus(); err != nil {
		return fmt.Errorf(
			"space create: failed to update space phase (%v): %v",
			spec.SpacePhaseActive,
			err,
		)
	}
	c.log.Infof("space is now active")
	return nil
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) internalCreate() error {
	httpCert, err := c.getHttpCert()
	if err != nil {
		return fmt.Errorf("failed to read http cert: %s", err)
	}
	tcpCertAndCa, err := c.getTcpCertAndCa()
	if err != nil {
		return fmt.Errorf("failed to read TCP cert and CA: %s", err)
	}
	if err = c.createNamespace(); err != nil {
		return fmt.Errorf("failed to create namespace: %s", err)
	}
	if err = c.createNetPolicy(); err != nil {
		return fmt.Errorf("failed to create network policy: %s", err)
	}
	if err = c.copySecret(httpCert); err != nil {
		return fmt.Errorf("failed to copy http cert: %s", err)
	}
	if err = c.deleteHttpCert(); err != nil {
		return fmt.Errorf("failed to delete http cert: %s", err)
	}
	if tcpCertAndCa != nil {
		if err = c.copySecret(tcpCertAndCa); err != nil {
			return fmt.Errorf("failed to copy tcp cert and CA: %s", err)
		}
		if err = c.deleteTcpCertAndCa(); err != nil {
			return fmt.Errorf("failed to delete tcp cert and CA: %s", err)
		}
	}
	if err = c.createHttpIngress(); err != nil {
		return fmt.Errorf("failed to create http ingress: %s", err)
	}
	if err = c.createDefaultHttpDeploy(); err != nil {
		return fmt.Errorf("failed to create default http dep: %s", err)
	}
	if err = c.createDefaultHttpSvc(); err != nil {
		return fmt.Errorf("failed to create default http svc: %s", err)
	}
	if err = c.createPrivateIngressController(); err != nil {
		return fmt.Errorf("failed to create private ingress controller: %s", err)
	}
	if err = c.updateDns(false); err != nil {
		return fmt.Errorf("failed to update DNS for space: %s", err)
	}
	return nil
}

// -----------------------------------------------------------------------------

func (c * SpaceRuntime) createNetPolicy() error {
	if c.Space.Spec.Project == "" {
		return nil
	}
	peers := []netv1.NetworkPolicyPeer{
		{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string {
					"decco-project": spec.RESERVED_PROJECT_NAME,
				},
			},
		},
		{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"decco-project": c.Space.Spec.Project,
				},
			},
		},
	}
	np := netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.Space.Name,
		},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			Ingress: []netv1.NetworkPolicyIngressRule{
				{
					From: peers,
				},
			},
		},
	}
	netApi := c.kubeApi.NetworkingV1().NetworkPolicies(c.Space.Name)
	_, err := netApi.Create(&np)
	return err
}

// -----------------------------------------------------------------------------

func (c * SpaceRuntime) updateDns(delete bool) error {
	if !dns.Enabled() {
		c.log.Debug("skipping DNS update: no registered provider")
		return nil
	}

	ipOrHostname, isHostname, err := k8sutil.GetTcpIngressIpOrHostname(c.kubeApi)
	if err != nil {
		return fmt.Errorf("failed to get TCP ingress ipOrHostname: %s", err)
	}
	expBackoff := misc.DefaultBackoff()
	url := os.Getenv("SLACK_WEBHOOK_FOR_DNS_UPDATE_FAILURE")
	attempt := 0
	verb := "create"
	if (delete) {
		verb = "delete"
	}
	updateFn := func () error {
		attempt += 1
		err := dns.UpdateRecord(c.Space.Spec.DomainName, c.Space.Name,
			ipOrHostname, isHostname, delete)
		if err != nil {
			msg := fmt.Sprintf("attempt %d to %s DNS for %s failed: %s",
				attempt, verb, c.Space.Name, err)
			c.log.Warnf(msg)
			err = fmt.Errorf("%s", msg)
			if url != "" {
				slack.PostBestEffort(url, msg, c.log)
			}
		} else if url != "" && attempt >= 2 {
			msg := fmt.Sprintf("DNS %s for %s succeeded after %d attempts",
				verb, c.Space.Name, attempt)
			slack.PostBestEffort(url, msg, c.log)
		}
		return err
	}
	return backoff.RetryNotify(updateFn, expBackoff, nil)
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) logCreation() {
	specBytes, err := json.MarshalIndent(c.Space.Spec, "", "    ")
	if err != nil {
		c.log.Errorf("failed to marshal cluster spec: %v", err)
		return
	}

	c.log.Info("creating space with Spec:")
	for _, m := range strings.Split(string(specBytes), "\n") {
		c.log.Info(m)
	}
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createNamespace() error {
	nsApi := c.kubeApi.CoreV1().Namespaces()
	ns := v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: c.Space.Name,
			Labels: map[string]string {
				"app": "decco",
			},
		},
	}
	if c.Space.Spec.Project != "" {
		ns.ObjectMeta.Labels["decco-project"] = c.Space.Spec.Project
	}
	_, err := nsApi.Create(&ns)
	return err
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createHttpIngress() error {
	hostName := c.Space.Name + "." + c.Space.Spec.DomainName
	defaultHttpSvcPort := int32(80)
	if c.Space.Spec.EncryptHttp {
		defaultHttpSvcPort = k8sutil.TlsPort
	}
	return k8sutil.CreateHttpIngress(
		c.kubeApi,
		c.Space.Name,
		"http-ingress",
		map[string]string {"app": "decco"},
		hostName,
		"/",
		"default-http",
		defaultHttpSvcPort,
		"",
		c.Space.Spec.EncryptHttp,
		c.Space.Spec.HttpCertSecretName,
		false,
	)
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createDefaultHttpDeploy() error {
	depApi := c.kubeApi.ExtensionsV1beta1().Deployments(c.Space.Name)
	var volumes []v1.Volume
	containers := []v1.Container {
		{
			Name: "default-http",
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
				Handler: v1.Handler {
					HTTPGet: &v1.HTTPGetAction{
						Path: "/healthz",
						Port: intstr.IntOrString {
							Type: intstr.Int,
							IntVal: defaultHttpInternalPort,
						},
						Scheme: "HTTP",
					},
				},
				InitialDelaySeconds: 30,
				TimeoutSeconds: 5,
			},
			Resources: v1.ResourceRequirements {
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

	if c.Space.Spec.EncryptHttp {
		destHostAndPort := fmt.Sprintf("%d", defaultHttpInternalPort)
		volumes, containers = k8sutil.InsertStunnel("stunnel",
			k8sutil.TlsPort,"no",
			destHostAndPort, "", c.Space.Spec.HttpCertSecretName,
			true, false, volumes,
			containers, 0, 0)
	} else {
		containers[0].Ports = []v1.ContainerPort{
			{ContainerPort: defaultHttpInternalPort },
		}
	}

	_, err := depApi.Create(&v1beta1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-http",
			Labels: map[string]string {
				"app": "decco",
			},
		},
		Spec: v1beta1.DeploymentSpec{
			Replicas: nil,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string {
					"app": "default-http",
				},
			},
			Template: v1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta {
					Name: "default-http",
					Labels: map[string]string {
						"app": "default-http",
					},
				},
				Spec: v1.PodSpec{
					Containers: containers,
					Volumes: volumes,
				},
			},
		},
	})
	return err
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createPrivateIngressController() error {
	if c.Space.Spec.DisablePrivateIngressController {
		return nil
	}
	svcAcctApi := c.kubeApi.CoreV1().ServiceAccounts(c.Space.Name)
	_, err := svcAcctApi.Create(&v1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nginx-ingress",
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create nginx-ingress svc acct: %s",
			err)
	}
	rolesApi := c.kubeApi.RbacV1().Roles(c.Space.Name)
	_, err = rolesApi.Create(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ingress-controller",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string {"*"},
				Resources: []string {"events"},
				Verbs: []string {"create"},
			},
			{
				APIGroups: []string {"*"},
				Resources: []string {"configmaps"},
				Verbs: []string {"create", "update", "get", "watch", "list"},
			},
			{
				APIGroups: []string {"*"},
				Resources: []string {"ingresses", "ingresses/status"},
				Verbs: []string {"update", "get", "watch", "list"},
			},
			{
				APIGroups: []string {"*"},
				Resources: []string {"pods", "services", "secrets",
					"namespaces", "endpoints"},
				Verbs: []string {"get", "watch", "list"},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create nginx-controller role: %s",
			err)
	}
	rbApi := c.kubeApi.RbacV1().RoleBindings(c.Space.Name)
	_, err = rbApi.Create(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nginx-ingress",
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

	hostName := c.Space.Name + "." + c.Space.Spec.DomainName
	config := k8sutil.GetClusterConfigOrDie()
	config.GroupVersion = &appspec.SchemeGroupVersion
	config.APIPath = "/apis"
	config.NegotiatedSerializer = serializer.DirectCodecFactory{
		CodecFactory: appspec.Codecs,
	}
	restCli, err := rest.RESTClientFor(config)
	if err != nil {
		return fmt.Errorf("failed to create app rest client: %s", err)
	}

	watchNsStr := fmt.Sprintf("--watch-namespace=%s", c.Space.Name)
	args := []string{
		"/nginx-ingress-controller",
		"--default-backend-service=$(POD_NAMESPACE)/default-http",
		"--http-only-ipv4-bindaddress-prefix=127.0.0.1:",
		"--enable-ssl-chain-completion=false",
		watchNsStr,
	}
	if c.Space.Spec.VerboseIngressControllerLogging ||
		os.Getenv("VERBOSE_INGRESS_CONTROLLER_LOGGING") != "" {
		args = append(args, "--v=5")
	} else {
		args = append(args, "--v=1")
	}
	baseTlsListenPort := int32(k8sutil.TlsPort)
	endpoints := []appspec.EndpointSpec{
		{
			Name: "nginx-ingress",
			Port: baseTlsListenPort,     // nginx itself terminates TLS
			DisableTlsTermination: true, // no stunnel side-car
			SniHostname: hostName,
		},
	}
	for _, epName := range c.Space.Spec.PrivateIngressControllerTcpEndpoints {
		baseTlsListenPort += 1
		endpoints = append(endpoints, appspec.EndpointSpec{
			Name: "nginx-ingress-sni-" + epName,
			Port: 80,
			TlsListenPort: baseTlsListenPort,   // use stunnel beginning at 444
			SniHostname: epName + "." + hostName,
		})
	}
	app := appspec.App{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nginx-ingress",
		},
		Spec: appspec.AppSpec {
			InitialReplicas: 1,
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
										FieldPath: "metadata.name",
									},
								},
							},
							{
								Name: "POD_NAMESPACE",
								ValueFrom: &v1.EnvVarSource{
									FieldRef: &v1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath: "metadata.namespace",
									},
								},
							},
						},
						//Image: "quay.io/kubernetes-ingress-controller/nginx-ingress-controller:0.19.0",
						Image: "platform9systems/nginx-ingress-controller:106",
						Ports: []v1.ContainerPort{
							{
								ContainerPort: int32(443),
							},
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								"cpu": resource.MustParse("100m"),
								"memory": resource.MustParse("32Mi"),
							},
						},
					},
				},
			},
			Endpoints: endpoints,
		},
	}
	var rtObj runtime.Object
	rtObj = &app
	err = restCli.Post().Namespace(c.Space.Name).
		Resource(appspec.CRDResourcePlural).
		Body(rtObj).Do().Into(nil)
	return err
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) createDefaultHttpSvc() error {
	svcApi := c.kubeApi.CoreV1().Services(c.Space.Name)
	svcPort := int32(80)
	tgtPort := defaultHttpInternalPort
	if c.Space.Spec.EncryptHttp {
		svcPort = k8sutil.TlsPort
		tgtPort = k8sutil.TlsPort
	}
	_, err := svcApi.Create(&v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default-http",
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{
					Port: svcPort,
					TargetPort: intstr.IntOrString {
						Type: intstr.Int,
						IntVal: tgtPort,
					},
				},
			},
			Selector: map[string]string {
				"app": "default-http",
			},
		},
	})
	return err
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) getHttpCert() (*v1.Secret, error) {
	secrApi := c.kubeApi.CoreV1().Secrets(c.namespace)
	return secrApi.Get(c.Space.Spec.HttpCertSecretName, metav1.GetOptions{})
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) deleteHttpCert() error {
	if !c.Space.Spec.DeleteHttpCertSecretAfterCopy {
		return nil
	}
	secrApi := c.kubeApi.CoreV1().Secrets(c.namespace)
	return secrApi.Delete(c.Space.Spec.HttpCertSecretName,
		&metav1.DeleteOptions{})
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) deleteTcpCertAndCa() error {
	if !c.Space.Spec.DeleteTcpCertAndCaSecretAfterCopy {
		return nil
	}
	secrApi := c.kubeApi.CoreV1().Secrets(c.namespace)
	return secrApi.Delete(c.Space.Spec.TcpCertAndCaSecretName,
		&metav1.DeleteOptions{})
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) getTcpCertAndCa() (*v1.Secret, error) {
	if c.Space.Spec.TcpCertAndCaSecretName == "" {
		return nil, nil
	}
	secrApi := c.kubeApi.CoreV1().Secrets(c.namespace)
	return secrApi.Get(c.Space.Spec.TcpCertAndCaSecretName, metav1.GetOptions{})
}

// -----------------------------------------------------------------------------

func (c *SpaceRuntime) copySecret(s *v1.Secret) error {
	newCertSecret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Data: s.Data,
		StringData: s.StringData,
	}
	secrApi := c.kubeApi.CoreV1().Secrets(c.Space.Name)
	_, err := secrApi.Create(&newCertSecret)
	return err
}
