/*
Copyright 2026.

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

package controller

import (
	"context"
	"encoding/json"
	"fmt"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

var _ = Describe("DappManifest Controller", func() {
	Context("When reconciling a DappManifest with a Helm spec", func() {
		const resourceName = "test-dapp"
		const repoURL = "https://stefanprodan.github.io/podinfo"
		const targetNS = "test-dapp-system"

		ctx := context.Background()

		namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}
		helmReleaseName := types.NamespacedName{Name: resourceName, Namespace: "default"}
		gatewayKey := types.NamespacedName{Name: gatewayName("node-a"), Namespace: "orca-system"}

		var reconciler *DappManifestReconciler

		newDappManifest := func(name string) *cachev1alpha1.DappManifest {
			return &cachev1alpha1.DappManifest{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: cachev1alpha1.DappManifestSpec{
					DappName:  name,
					Namespace: targetNS,
					NodeName:  "node-a",
					Helm: cachev1alpha1.HelmSpec{
						ReleaseName: name,
						ChartName:   "podinfo",
						Version:     ">=6.0.0",
						RepoURL:     repoURL,
					},
				},
			}
		}

		// cleanupDappManifest deletes dm and what its reconcile created (envtest has no GC).
		// Deletion logic lands in Task 5, so the finalizer is removed by hand.
		cleanupDappManifest := func(key types.NamespacedName) {
			dm := &cachev1alpha1.DappManifest{}
			if err := k8sClient.Get(ctx, key, dm); err == nil {
				Expect(k8sClient.Delete(ctx, dm)).To(Succeed())
				patch := client.MergeFrom(dm.DeepCopy())
				dm.Finalizers = nil
				Expect(client.IgnoreNotFound(k8sClient.Patch(ctx, dm, patch))).To(Succeed())
			}
			for _, obj := range []client.Object{
				&helmv2.HelmRelease{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}},
				&sourcev1.HelmRepository{ObjectMeta: metav1.ObjectMeta{Name: key.Name + "-helmrepo", Namespace: key.Namespace}},
				&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: key.Name + "-e3-token", Namespace: targetNS}},
			} {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
			}
		}

		reconcileOnce := func(key types.NamespacedName) error {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			return err
		}

		// reconcileWithGateway reconciles, marks the Gateway available, and reconciles again.
		reconcileWithGateway := func(key types.NamespacedName) error {
			Expect(reconcileOnce(key)).To(Succeed())
			dep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, gatewayKey, dep)).To(Succeed())
			setDeploymentAvailable(dep, dep.Generation)
			return reconcileOnce(key)
		}

		getDM := func(key types.NamespacedName) *cachev1alpha1.DappManifest {
			dm := &cachev1alpha1.DappManifest{}
			Expect(k8sClient.Get(ctx, key, dm)).To(Succeed())
			return dm
		}

		getInventory := func() *cachev1alpha1.DappInventory {
			inv := &cachev1alpha1.DappInventory{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "node-a"}, inv)).To(Succeed())
			return inv
		}

		getEntry := func(name string) cachev1alpha1.DappInventoryEntry {
			inv := getInventory()
			i := findEntry(inv.Spec.Entries, cachev1alpha1.DappRef{Namespace: "default", Name: name})
			Expect(i).To(BeNumerically(">=", 0))
			return inv.Spec.Entries[i]
		}

		getSecret := func(name string) *corev1.Secret {
			s := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name + "-e3-token", Namespace: targetNS}, s)).To(Succeed())
			return s
		}

		condStatus := func(dm *cachev1alpha1.DappManifest, t string) metav1.ConditionStatus {
			c := findCondition(dm.Status.Conditions, t)
			Expect(c).NotTo(BeNil(), t)
			return c.Status
		}

		BeforeEach(func() {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "orca-system"}}
			if err := k8sClient.Create(ctx, ns); err != nil && !errors.IsAlreadyExists(err) {
				Fail(err.Error())
			}
			reconciler = &DappManifestReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(),
				GatewayImage: "example.com/gw:test", GatewayNamespace: "orca-system"}

			By("creating the DappManifest resource")
			Expect(k8sClient.Create(ctx, newDappManifest(resourceName))).To(Succeed())
		})

		AfterEach(func() {
			By("deleting the DappManifest resource and what it created")
			cleanupDappManifest(namespacedName)
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: gatewayKey.Name, Namespace: gatewayKey.Namespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dep))).To(Succeed())
			inv := &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inv))).To(Succeed())
			// The target namespace is kept: envtest has no namespace controller, so a deleted
			// namespace stays Terminating and the next spec could not create Secrets in it.
		})

		It("rejects changing nodeName", func() {
			dm := getDM(namespacedName)
			dm.Spec.NodeName = "node-b"
			err := k8sClient.Update(ctx, dm)
			Expect(err).To(MatchError(ContainSubstring("nodeName is immutable")))
		})

		It("assigns a dApp ID, stores only the token hash, and creates the Secret", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())

			secret := getSecret(resourceName)
			Expect(secret.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "orca"))
			entry := getEntry(resourceName)
			Expect(entry.DappID).To(Equal(int32(1)))
			Expect(entry.TokenHash).To(Equal(hashToken(string(secret.Data["token"]))))

			helmRelease := &helmv2.HelmRelease{}
			Expect(k8sClient.Get(ctx, helmReleaseName, helmRelease)).To(Succeed())
			Expect(helmRelease.Spec.TargetNamespace).To(Equal(targetNS))
			Expect(helmRelease.Spec.PostRenderers[0].Kustomize.Patches[0].Patch).To(ContainSubstring("nodeName: node-a"))
			var values map[string]map[string]string
			Expect(json.Unmarshal(helmRelease.Spec.Values.Raw, &values)).To(Succeed())
			Expect(values["orca"]["e3TokenSecret"]).To(Equal(resourceName + "-e3-token"))

			dm := getDM(namespacedName)
			Expect(dm.Finalizers).To(ContainElement(dappFinalizer))
			Expect(dm.Status.DappID).To(Equal(int32(1)))
			Expect(dm.Status.NodeName).To(Equal("node-a"))
		})

		It("reports Ready only after the Gateway registers and advertises", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())

			dm := getDM(namespacedName)
			Expect(condStatus(dm, "Ready")).To(Equal(metav1.ConditionFalse))
			Expect(condStatus(dm, "GatewayReady")).To(Equal(metav1.ConditionTrue))
			Expect(condStatus(dm, "Registered")).To(Equal(metav1.ConditionFalse))

			inv := getInventory()
			inv.Status.Entries = []cachev1alpha1.DappInventoryEntryStatus{
				{DappID: 1, Generation: getEntry(resourceName).Generation, Registered: true, Advertised: true},
			}
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())

			helmRelease := &helmv2.HelmRelease{}
			Expect(k8sClient.Get(ctx, helmReleaseName, helmRelease)).To(Succeed())
			apimeta.SetStatusCondition(&helmRelease.Status.Conditions, metav1.Condition{
				Type: "Ready", Status: metav1.ConditionTrue, Reason: "InstallSucceeded", Message: "installed"})
			Expect(k8sClient.Status().Update(ctx, helmRelease)).To(Succeed())

			Expect(reconcileOnce(namespacedName)).To(Succeed())
			dm = getDM(namespacedName)
			for _, t := range []string{"Registered", "Advertised", "Installed", "Ready"} {
				Expect(condStatus(dm, t)).To(Equal(metav1.ConditionTrue), t)
			}
		})

		It("rotates the token when the spec changes", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			before := getEntry(resourceName)
			inv := getInventory()
			inv.Status.Entries = []cachev1alpha1.DappInventoryEntryStatus{
				{DappID: 1, Generation: before.Generation, Registered: true, Advertised: true},
			}
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())

			dm := getDM(namespacedName)
			dm.Spec.Helm.Version = ">=6.1.0"
			Expect(k8sClient.Update(ctx, dm)).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())

			dm = getDM(namespacedName)
			after := getEntry(resourceName)
			Expect(after.DappID).To(Equal(before.DappID))
			Expect(after.Generation).To(Equal(dm.Generation))
			Expect(after.TokenHash).NotTo(Equal(before.TokenHash))
			Expect(after.TokenHash).To(Equal(hashToken(string(getSecret(resourceName).Data["token"]))))
			Expect(condStatus(dm, "Registered")).To(Equal(metav1.ConditionFalse))
			Expect(condStatus(dm, "Advertised")).To(Equal(metav1.ConditionFalse))
		})

		It("rotates the token when the Secret is missing", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			before := getEntry(resourceName)
			Expect(k8sClient.Delete(ctx, getSecret(resourceName))).To(Succeed())

			Expect(reconcileOnce(namespacedName)).To(Succeed())
			after := getEntry(resourceName)
			Expect(after.TokenHash).NotTo(Equal(before.TokenHash))
			Expect(after.TokenHash).To(Equal(hashToken(string(getSecret(resourceName).Data["token"]))))
		})

		It("gives distinct IDs to two dApps on the same node", func() {
			second := types.NamespacedName{Name: resourceName + "-2", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newDappManifest(second.Name))).To(Succeed())
			DeferCleanup(cleanupDappManifest, second)

			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())

			Expect(getDM(namespacedName).Status.DappID).To(Equal(int32(1)))
			Expect(getDM(second).Status.DappID).To(Equal(int32(2)))
			Expect(getInventory().Spec.Entries).To(HaveLen(2))

			deps := &appsv1.DeploymentList{}
			Expect(k8sClient.List(ctx, deps, client.InNamespace("orca-system"))).To(Succeed())
			n := 0
			for _, d := range deps.Items {
				if d.Spec.Template.Spec.NodeName == "node-a" {
					n++
				}
			}
			Expect(n).To(Equal(1))
		})

		It("fails with InventoryUpdateFailed when the node is full", func() {
			inv := &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			for id := int32(1); id <= 100; id++ {
				inv.Spec.Entries = append(inv.Spec.Entries, cachev1alpha1.DappInventoryEntry{
					Dapp:   cachev1alpha1.DappRef{Namespace: "other", Name: fmt.Sprintf("d%d", id)},
					DappID: id, TokenHash: "x", Generation: 1,
				})
			}
			Expect(k8sClient.Create(ctx, inv)).To(Succeed())

			Expect(reconcileWithGateway(namespacedName)).NotTo(Succeed())

			ready := findCondition(getDM(namespacedName).Status.Conditions, "Ready")
			Expect(ready).NotTo(BeNil())
			Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			Expect(ready.Reason).To(Equal("InventoryUpdateFailed"))
			err := k8sClient.Get(ctx, helmReleaseName, &helmv2.HelmRelease{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
