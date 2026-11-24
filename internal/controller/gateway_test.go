package controller

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

var _ = Describe("ensureGateway", func() {
	var r *DappManifestReconciler

	BeforeEach(func() {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "orca-system"}}
		if err := k8sClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			Fail(err.Error())
		}
		r = &DappManifestReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(),
			GatewayImage: "example.com/gw:test", GatewayNamespace: "orca-system"}
	})

	// newInventory creates the inventory and removes it and its Gateway afterwards (envtest has no GC).
	newInventory := func(name string) *cachev1alpha1.DappInventory {
		inv := &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: name}}
		Expect(k8sClient.Create(ctx, inv)).To(Succeed())
		DeferCleanup(func() {
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: gatewayName(name), Namespace: "orca-system"}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dep))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inv))).To(Succeed())
		})
		return inv
	}

	It("creates a Gateway Deployment owned by the inventory and reports readiness", func() {
		inv := newInventory("node-a")

		ready, err := r.ensureGateway(ctx, inv)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())

		dep := &appsv1.Deployment{}
		key := types.NamespacedName{Name: "orca-gateway-node-a", Namespace: "orca-system"}
		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		Expect(*dep.Spec.Replicas).To(Equal(int32(1)))
		Expect(dep.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType))
		pod := dep.Spec.Template.Spec
		Expect(pod.NodeName).To(Equal("node-a"))
		Expect(pod.ServiceAccountName).To(Equal("orca-gateway"))
		Expect(pod.Containers[0].Image).To(Equal("example.com/gw:test"))
		Expect(pod.Containers[0].Env[0].Name).To(Equal("NODE_NAME"))
		Expect(pod.Containers[0].Env[0].ValueFrom.FieldRef.FieldPath).To(Equal("spec.nodeName"))
		Expect(dep.OwnerReferences).To(HaveLen(1))
		Expect(dep.OwnerReferences[0].Name).To(Equal("node-a"))
		Expect(*dep.OwnerReferences[0].Controller).To(BeTrue())

		// Available replicas but the controller has not observed the latest generation: not ready.
		dep.Status.Replicas, dep.Status.UpdatedReplicas = 1, 1
		dep.Status.ReadyReplicas, dep.Status.AvailableReplicas = 1, 1
		dep.Status.ObservedGeneration = dep.Generation - 1
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		ready, err = r.ensureGateway(ctx, inv)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeFalse())

		Expect(k8sClient.Get(ctx, key, dep)).To(Succeed())
		dep.Status.ObservedGeneration = dep.Generation
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
		ready, err = r.ensureGateway(ctx, inv)
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(BeTrue())
	})

	It("accepts a node name longer than the 63 character label limit", func() {
		inv := newInventory(strings.Repeat("n", 70))
		_, err := r.ensureGateway(ctx, inv)
		Expect(err).NotTo(HaveOccurred())
	})
})
