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

		newDappManifest := func(key types.NamespacedName) *cachev1alpha1.DappManifest {
			return &cachev1alpha1.DappManifest{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: cachev1alpha1.DappManifestSpec{
					DappName:  key.Name,
					Namespace: targetNS,
					NodeName:  "node-a",
					Helm: cachev1alpha1.HelmSpec{
						ReleaseName: key.Name,
						ChartName:   "podinfo",
						Version:     ">=6.0.0",
						RepoURL:     repoURL,
					},
				},
			}
		}

		reconcileOnce := func(key types.NamespacedName) error {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			return err
		}

		// withdrawAll plays the Gateway confirming the withdrawal of every dApp on node-a,
		// which it does by removing their status entries.
		withdrawAll := func() {
			inv := &cachev1alpha1.DappInventory{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: "node-a"}, inv); err != nil {
				return
			}
			inv.Status.Entries = nil
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())
		}

		// deleteAndReconcile deletes dm and reconciles until the finalizer lets it go.
		deleteAndReconcile := func(key types.NamespacedName) {
			dm := &cachev1alpha1.DappManifest{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dm))).To(Succeed())
			Eventually(func() bool {
				_ = reconcileOnce(key)
				return errors.IsNotFound(k8sClient.Get(ctx, key, &cachev1alpha1.DappManifest{}))
			}).Should(BeTrue())
		}

		// cleanupDappManifest deletes dm through the finalizer, then its Flux objects (envtest has no GC).
		cleanupDappManifest := func(key types.NamespacedName) {
			withdrawAll()
			deleteAndReconcile(key)
			for _, obj := range []client.Object{
				&helmv2.HelmRelease{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}},
				&sourcev1.HelmRepository{ObjectMeta: metav1.ObjectMeta{Name: key.Name + "-helmrepo", Namespace: key.Namespace}},
			} {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, obj))).To(Succeed())
			}
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

		getEntry := func(key types.NamespacedName) cachev1alpha1.DappInventoryEntry {
			inv := getInventory()
			i := findEntry(inv.Spec.Entries, cachev1alpha1.DappRef{Namespace: key.Namespace, Name: key.Name})
			Expect(i).To(BeNumerically(">=", 0))
			return inv.Spec.Entries[i]
		}

		// currentSecretName is the Secret named after the entry's current token hash.
		currentSecretName := func(key types.NamespacedName) string {
			return tokenSecretName(getDM(key), getEntry(key).TokenHash)
		}

		getSecret := func(name string) *corev1.Secret {
			s := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: targetNS}, s)).To(Succeed())
			return s
		}

		helmValuesSecret := func(key types.NamespacedName) string {
			hr := &helmv2.HelmRelease{}
			Expect(k8sClient.Get(ctx, key, hr)).To(Succeed())
			var values map[string]map[string]string
			Expect(json.Unmarshal(hr.Spec.Values.Raw, &values)).To(Succeed())
			return values["orca"]["e3TokenSecret"]
		}

		condStatus := func(dm *cachev1alpha1.DappManifest, t string) metav1.ConditionStatus {
			c := findCondition(dm.Status.Conditions, t)
			Expect(c).NotTo(BeNil(), t)
			return c.Status
		}

		setGatewayStatus := func(gen int64) {
			inv := getInventory()
			inv.Status.Entries = []cachev1alpha1.DappInventoryEntryStatus{
				{DappID: 1, Generation: gen, Registered: true, Advertised: true},
			}
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())
		}

		readyReason := func(key types.NamespacedName) string {
			c := findCondition(getDM(key).Status.Conditions, "Ready")
			Expect(c).NotTo(BeNil())
			return c.Reason
		}

		// setAdvertisedIDs plays the Gateway: one Registered and Advertised status entry per ID,
		// and none for the IDs it has withdrawn.
		setAdvertisedIDs := func(ids ...int32) {
			inv := getInventory()
			inv.Status.Entries = nil
			for _, id := range ids {
				inv.Status.Entries = append(inv.Status.Entries,
					cachev1alpha1.DappInventoryEntryStatus{DappID: id, Generation: 1, Registered: true, Advertised: true})
			}
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())
		}

		BeforeEach(func() {
			for _, name := range []string{"orca-system", "other"} {
				ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
				if err := k8sClient.Create(ctx, ns); err != nil && !errors.IsAlreadyExists(err) {
					Fail(err.Error())
				}
			}
			reconciler = &DappManifestReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: k8sClient.Scheme(),
				GatewayImage: "example.com/gw:test", GatewayNamespace: "orca-system"}

			By("creating the DappManifest resource")
			Expect(k8sClient.Create(ctx, newDappManifest(namespacedName))).To(Succeed())
		})

		AfterEach(func() {
			By("deleting the DappManifest resource and what it created")
			cleanupDappManifest(namespacedName)
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: gatewayKey.Name, Namespace: gatewayKey.Namespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dep))).To(Succeed())
			inv := &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: "node-a"}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, inv))).To(Succeed())
			Expect(k8sClient.DeleteAllOf(ctx, &corev1.Secret{}, client.InNamespace(targetNS),
				client.MatchingLabels{ManagedByLabel: "orca"})).To(Succeed())
			// Namespaces are kept: envtest has no namespace controller, so a deleted
			// namespace stays Terminating and the next spec could not create Secrets in it.
		})

		It("rejects changing nodeName", func() {
			dm := getDM(namespacedName)
			dm.Spec.NodeName = "node-b"
			err := k8sClient.Update(ctx, dm)
			Expect(err).To(MatchError(ContainSubstring("nodeName is immutable")))
		})

		It("rejects changing namespace", func() {
			dm := getDM(namespacedName)
			dm.Spec.Namespace = "elsewhere"
			err := k8sClient.Update(ctx, dm)
			Expect(err).To(MatchError(ContainSubstring("namespace is immutable")))
		})

		It("rejects a DappManifest without namespace", func() {
			dm := newDappManifest(types.NamespacedName{Name: resourceName + "-nons", Namespace: "default"})
			dm.Spec.Namespace = ""
			Expect(k8sClient.Create(ctx, dm)).To(MatchError(ContainSubstring("spec.namespace")))
		})

		It("assigns a dApp ID, stores only the token hash, and creates the Secret", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())

			dm := getDM(namespacedName)
			entry := getEntry(namespacedName)
			Expect(entry.DappID).To(Equal(int32(1)))
			Expect(entry.Generation).To(Equal(int64(1)))

			name := currentSecretName(namespacedName)
			Expect(name).To(Equal("default-test-dapp-e3-" + entry.TokenHash[:10]))
			secret := getSecret(name)
			Expect(secret.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "orca"))
			Expect(secret.Labels).To(HaveKeyWithValue("orca.cache.orca.com/dapp-uid", string(dm.UID)))
			Expect(secret.Annotations).To(HaveKeyWithValue("orca.cache.orca.com/dapp-generation", "1"))
			Expect(entry.TokenHash).To(Equal(hashToken(string(secret.Data["token"]))))

			helmRelease := &helmv2.HelmRelease{}
			Expect(k8sClient.Get(ctx, helmReleaseName, helmRelease)).To(Succeed())
			Expect(helmRelease.Spec.TargetNamespace).To(Equal(targetNS))
			Expect(helmRelease.Spec.PostRenderers[0].Kustomize.Patches[0].Patch).To(ContainSubstring("nodeName: node-a"))
			Expect(helmValuesSecret(helmReleaseName)).To(Equal(name))

			Expect(dm.Finalizers).To(ContainElement(dappFinalizer))
			Expect(dm.Status.DappID).To(Equal(int32(1)))
			Expect(dm.Status.NodeName).To(Equal("node-a"))
		})

		It("does not rotate when nothing changed", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			before := getEntry(namespacedName)
			name := currentSecretName(namespacedName)

			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(getEntry(namespacedName)).To(Equal(before))
			Expect(helmValuesSecret(helmReleaseName)).To(Equal(name))
			getSecret(name)
		})

		It("reports Ready only after the Gateway registers and advertises", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())

			dm := getDM(namespacedName)
			Expect(condStatus(dm, "Ready")).To(Equal(metav1.ConditionFalse))
			Expect(condStatus(dm, "GatewayReady")).To(Equal(metav1.ConditionTrue))
			Expect(condStatus(dm, "Registered")).To(Equal(metav1.ConditionFalse))

			setGatewayStatus(getEntry(namespacedName).Generation)

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

		It("rotates the token into a new Secret when the spec changes", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			before := getEntry(namespacedName)
			oldName := currentSecretName(namespacedName)
			setGatewayStatus(before.Generation)

			dm := getDM(namespacedName)
			dm.Spec.Helm.Version = ">=6.1.0"
			Expect(k8sClient.Update(ctx, dm)).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())

			dm = getDM(namespacedName)
			after := getEntry(namespacedName)
			newName := currentSecretName(namespacedName)
			Expect(after.DappID).To(Equal(before.DappID))
			Expect(after.Generation).To(Equal(before.Generation + 1))
			Expect(after.TokenHash).NotTo(Equal(before.TokenHash))
			Expect(newName).NotTo(Equal(oldName))
			Expect(helmValuesSecret(helmReleaseName)).To(Equal(newName))
			Expect(after.TokenHash).To(Equal(hashToken(string(getSecret(newName).Data["token"]))))
			err := k8sClient.Get(ctx, types.NamespacedName{Name: oldName, Namespace: targetNS}, &corev1.Secret{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
			Expect(condStatus(dm, "Registered")).To(Equal(metav1.ConditionFalse))
			Expect(condStatus(dm, "Advertised")).To(Equal(metav1.ConditionFalse))
		})

		It("rotates the token when the Secret is missing", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			before := getEntry(namespacedName)
			setGatewayStatus(before.Generation)
			Expect(k8sClient.Delete(ctx, getSecret(currentSecretName(namespacedName)))).To(Succeed())

			Expect(reconcileOnce(namespacedName)).To(Succeed())
			after := getEntry(namespacedName)
			Expect(after.Generation).To(Equal(before.Generation + 1))
			Expect(after.TokenHash).NotTo(Equal(before.TokenHash))
			Expect(after.TokenHash).To(Equal(hashToken(string(getSecret(currentSecretName(namespacedName)).Data["token"]))))
			Expect(condStatus(getDM(namespacedName), "Registered")).To(Equal(metav1.ConditionFalse))
		})

		It("gives distinct IDs to two dApps on the same node", func() {
			second := types.NamespacedName{Name: resourceName + "-2", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newDappManifest(second))).To(Succeed())
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

		It("keeps separate stable Secrets for same-named dApps in different namespaces", func() {
			other := types.NamespacedName{Name: resourceName, Namespace: "other"}
			Expect(k8sClient.Create(ctx, newDappManifest(other))).To(Succeed())
			DeferCleanup(cleanupDappManifest, other)

			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			Expect(reconcileOnce(other)).To(Succeed())
			first, second := getEntry(namespacedName), getEntry(other)
			Expect(currentSecretName(namespacedName)).NotTo(Equal(currentSecretName(other)))

			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(reconcileOnce(other)).To(Succeed())
			Expect(getEntry(namespacedName)).To(Equal(first))
			Expect(getEntry(other)).To(Equal(second))
			getSecret(currentSecretName(namespacedName))
			getSecret(currentSecretName(other))
		})

		It("waits for the Gateway to withdraw before removing the finalizer", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			setGatewayStatus(getEntry(namespacedName).Generation)
			secretName := currentSecretName(namespacedName)

			Expect(k8sClient.Delete(ctx, getDM(namespacedName))).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(getInventory().Spec.Entries).To(BeEmpty())
			Expect(readyReason(namespacedName)).To(Equal("WaitingForWithdraw"))
			getSecret(secretName)

			By("waiting on the persisted dApp ID once the entry is gone")
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(readyReason(namespacedName)).To(Equal("WaitingForWithdraw"))
			getSecret(secretName)

			withdrawAll()
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			err := k8sClient.Get(ctx, namespacedName, &cachev1alpha1.DappManifest{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
			err = k8sClient.Get(ctx, types.NamespacedName{Name: secretName, Namespace: targetNS}, &corev1.Secret{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "node-a"}, &cachev1alpha1.DappInventory{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("deletes a never-advertised dApp without waiting", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())

			Expect(k8sClient.Delete(ctx, getDM(namespacedName))).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			err := k8sClient.Get(ctx, namespacedName, &cachev1alpha1.DappManifest{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("keeps the inventory while another dApp remains on the node", func() {
			second := types.NamespacedName{Name: resourceName + "-2", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newDappManifest(second))).To(Succeed())
			DeferCleanup(cleanupDappManifest, second)
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())

			deleteAndReconcile(namespacedName)
			entries := getInventory().Spec.Entries
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Dapp.Name).To(Equal(second.Name))
		})

		// waitsWhileOtherDappRemains deletes the first dApp while a second one stays on node-a, so the
		// spec never empties and only the deleting dApp's own status entry can hold its finalizer.
		waitsWhileOtherDappRemains := func(own cachev1alpha1.DappInventoryEntryStatus) {
			second := types.NamespacedName{Name: resourceName + "-2", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newDappManifest(second))).To(Succeed())
			DeferCleanup(cleanupDappManifest, second)
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())
			others := []cachev1alpha1.DappInventoryEntryStatus{{DappID: 2, Generation: 1, Registered: true, Advertised: true}}
			inv := getInventory()
			own.DappID, own.Generation = 1, getEntry(namespacedName).Generation
			inv.Status.Entries = append([]cachev1alpha1.DappInventoryEntryStatus{own}, others...)
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())

			Expect(k8sClient.Delete(ctx, getDM(namespacedName))).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(readyReason(namespacedName)).To(Equal("WaitingForWithdraw"))
			entries := getInventory().Spec.Entries
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Dapp.Name).To(Equal(second.Name))

			By("the Gateway removes the deleting dApp's status entry")
			inv = getInventory()
			inv.Status.Entries = others
			Expect(k8sClient.Status().Update(ctx, inv)).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			err := k8sClient.Get(ctx, namespacedName, &cachev1alpha1.DappManifest{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
			Expect(getInventory().Spec.Entries).To(HaveLen(1))
		}

		It("waits for its own status entry to go while another dApp stays on the node", func() {
			waitsWhileOtherDappRemains(cachev1alpha1.DappInventoryEntryStatus{Registered: true, Advertised: true})
		})

		It("waits for its own status entry to go when deleted while the add is in flight", func() {
			waitsWhileOtherDappRemains(cachev1alpha1.DappInventoryEntryStatus{Registered: true, Advertised: false})
		})

		It("holds every deleting dApp on an emptying node until the Gateway status is empty", func() {
			second := types.NamespacedName{Name: resourceName + "-2", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newDappManifest(second))).To(Succeed())
			DeferCleanup(cleanupDappManifest, second)
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())
			setAdvertisedIDs(1, 2)

			Expect(k8sClient.Delete(ctx, getDM(namespacedName))).To(Succeed())
			Expect(k8sClient.Delete(ctx, getDM(second))).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())
			Expect(getInventory().Spec.Entries).To(BeEmpty())

			By("withdrawing only the first dApp")
			setAdvertisedIDs(2)
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())
			Expect(readyReason(namespacedName)).To(Equal("WaitingForWithdraw"))
			Expect(findCondition(getDM(namespacedName).Status.Conditions, "Ready").Message).To(
				Equal("Waiting for the Gateway to withdraw the remaining dApps on the node"))
			Expect(readyReason(second)).To(Equal("WaitingForWithdraw"))
			Expect(findCondition(getDM(second).Status.Conditions, "Ready").Message).To(
				Equal("Waiting for the Gateway to withdraw the dApp"))
			getInventory()

			By("withdrawing the second dApp")
			setAdvertisedIDs()
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(reconcileOnce(second)).To(Succeed())
			for _, key := range []types.NamespacedName{namespacedName, second} {
				err := k8sClient.Get(ctx, key, &cachev1alpha1.DappManifest{})
				Expect(errors.IsNotFound(err)).To(BeTrue(), key.Name)
			}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "node-a"}, &cachev1alpha1.DappInventory{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})

		It("stops waiting when another dApp now holds the deleting dApp's ID", func() {
			Expect(reconcileWithGateway(namespacedName)).To(Succeed())
			setGatewayStatus(getEntry(namespacedName).Generation)
			Expect(k8sClient.Delete(ctx, getDM(namespacedName))).To(Succeed())
			Expect(reconcileOnce(namespacedName)).To(Succeed())
			Expect(readyReason(namespacedName)).To(Equal("WaitingForWithdraw"))

			By("the Gateway withdrew ID 1, another dApp took it and registered under it")
			inv := getInventory()
			inv.Spec.Entries = append(inv.Spec.Entries, cachev1alpha1.DappInventoryEntry{
				Dapp: cachev1alpha1.DappRef{Namespace: "other", Name: "newcomer"}, DappID: 1, TokenHash: "x", Generation: 2})
			Expect(k8sClient.Update(ctx, inv)).To(Succeed())

			Expect(reconcileOnce(namespacedName)).To(Succeed())
			err := k8sClient.Get(ctx, namespacedName, &cachev1alpha1.DappManifest{})
			Expect(errors.IsNotFound(err)).To(BeTrue())
			Expect(getInventory().Spec.Entries).To(HaveLen(1))
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
