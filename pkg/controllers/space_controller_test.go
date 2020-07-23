package controllers

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deccov1beta2 "github.com/platform9/decco/api/v1beta2"
	"github.com/platform9/decco/pkg/decco"
	"github.com/platform9/decco/pkg/dns"
	// +kubebuilder:scaffold:imports
)

var _ = Describe("Space Controller", func() {
	ctx := context.TODO()

	It("should reconcile a correct space", func() {
		stubSecret := &v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "stub-secret",
				Namespace: "default",
			},
			StringData: map[string]string{
				"tls.crt": "base64EncodedCertificate",
				"tls.key": "base64EncodedKey",
			},
			Type: v1.SecretTypeTLS,
		}
		err := k8sClient.Create(ctx, stubSecret)
		Expect(err).ToNot(HaveOccurred())

		space := &deccov1beta2.Space{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "correct-space",
				Namespace: "default",
			},
			Spec: deccov1beta2.SpaceSpec{
				DomainName:         "foo.example.com",
				HttpCertSecretName: "stub-secret",
			},
		}
		err = k8sClient.Create(ctx, space)
		Expect(err).ToNot(HaveOccurred())

		// Run one once through the reconciliation logic
		ingressClient := decco.NewStaticIngress("decco.example.com")
		dnsClient := dns.NewFakeProvider(nil)
		spaceCtrl := NewSpaceReconciler(k8sClient, dnsClient, ingressClient, nil)
		_, err = spaceCtrl.Reconcile(ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      space.Name,
				Namespace: space.Namespace,
			},
		})
		Expect(err).ToNot(HaveOccurred())

		// Fetch and check the status of the Space
		updatedSpace := &deccov1beta2.Space{}
		key, err := client.ObjectKeyFromObject(space)
		Expect(err).ToNot(HaveOccurred())
		err = k8sClient.Get(ctx, key, updatedSpace)
		Expect(err).ToNot(HaveOccurred())
		Expect(updatedSpace.Status.Namespace).To(Equal(space.Name))
		Expect(updatedSpace.Status.Phase).To(Equal(deccov1beta2.SpacePhaseActive))
		Expect(updatedSpace.Status.Hostname).To(Equal(fmt.Sprintf("%s.%s", space.Name, space.Spec.DomainName)))

		// Check if the secret was copied
		copiedSecret := &v1.Secret{}
		err = k8sClient.Get(ctx, types.NamespacedName{
			Name:      stubSecret.Name,
			Namespace: updatedSpace.Status.Namespace,
		}, copiedSecret)
		Expect(err).ToNot(HaveOccurred())
		Expect(copiedSecret.Type).To(Equal(stubSecret.Type))
		Expect(copiedSecret.Data).To(Equal(stubSecret.Data))
	})
})

/*
To test:
- invalid: missing domainName
- invalid: missing cert
- check dns configured
- check RBAC rules
- check certs
- check ingress + default-http

To do:
- validation
- conditions
*/
