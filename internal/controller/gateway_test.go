package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

var _ = Describe("ensureGateway", func() {
	It("creates a Gateway Deployment owned by the inventory and reports readiness", func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "orca-system"}}
		if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		inv := &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
		Expect(k8sClient.Create(ctx, inv)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, inv) })

		r := &DappManifestReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(),
			GatewayImage: "example.com/gw:test", GatewayNamespace: "orca-system"}

		ready, err := r.ensureGateway(ctx, inv)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())

		dep := &appsv1.Deployment{}
		key := types.NamespacedName{Name: "orca-gateway-node-a", Namespace: "orca-system"}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		Expect(dep.Spec.Template.Spec.NodeName).To(Equal("node-a"))
		Expect(dep.Spec.Template.Spec.ServiceAccountName).To(Equal("orca-gateway"))
		Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal("example.com/gw:test"))
		Expect(dep.OwnerReferences[0].Name).To(Equal("node-a"))

		dep.Status.Replicas = 1
		dep.Status.ReadyReplicas = 1
		dep.Status.AvailableReplicas = 1
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		ready, err = r.ensureGateway(ctx, inv)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
	})
})
