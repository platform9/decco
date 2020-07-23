package k8sutil

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func StripClusterDataFromObjectMeta(meta metav1.ObjectMeta) metav1.ObjectMeta {
	meta.ResourceVersion = ""
	meta.SelfLink = ""
	meta.UID = ""
	meta.ManagedFields = nil
	meta.CreationTimestamp = metav1.Time{}
	delete(meta.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	return meta
}

func ToNamespacedName(meta metav1.ObjectMeta) types.NamespacedName {
	return types.NamespacedName{
		Namespace: meta.Namespace,
		Name:      meta.Name,
	}
}
