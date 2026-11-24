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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxkustomize "github.com/fluxcd/pkg/apis/kustomize"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	sigsyaml "sigs.k8s.io/yaml"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

const dappFinalizer = "cache.orca.com/dapp-inventory"

// DappManifestReconciler reconciles a DappManifest object
type DappManifestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads token Secrets uncached, so cache lag never triggers a rotation.
	APIReader client.Reader

	// GatewayImage and GatewayNamespace configure the per-node Gateway Deployment.
	GatewayImage     string
	GatewayNamespace string
}

// +kubebuilder:rbac:groups=cache.orca.com,resources=dappmanifests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.orca.com,resources=dappmanifests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cache.orca.com,resources=dappmanifests/finalizers,verbs=update
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=helmrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.orca.com,resources=dappinventories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.orca.com,resources=dappinventories/status,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;create

func (r *DappManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	dm := &cachev1alpha1.DappManifest{}
	if err := r.Get(ctx, req.NamespacedName, dm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.Info("reconciling dappmanifest", "name", req.NamespacedName)

	if !dm.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, dm)
	}

	writeStatus := func() (ctrl.Result, error) {
		if err := r.Status().Update(ctx, dm); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	fail := func(reason string, err error) (ctrl.Result, error) {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "reconcile failed", "reason", reason)
		r.setReadyCondition(dm, metav1.ConditionFalse, reason, err.Error())
		_ = r.Status().Update(ctx, dm)
		return ctrl.Result{}, err
	}

	if controllerutil.AddFinalizer(dm, dappFinalizer) {
		if err := r.Update(ctx, dm); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
	}

	inv, err := r.getOrCreateInventory(ctx, dm.Spec.NodeName)
	if apierrors.IsAlreadyExists(err) {
		return ctrl.Result{Requeue: true}, nil
	}
	if err != nil {
		return fail("InventoryUpdateFailed", err)
	}

	ready, err := r.ensureGateway(ctx, inv)
	if err != nil {
		return fail("GatewayFailed", err)
	}
	if !ready {
		// The Gateway Deployment watch triggers the next reconcile.
		r.setCondition(dm, "GatewayReady", metav1.ConditionFalse, "Pending", "Waiting for the Gateway on node "+dm.Spec.NodeName)
		r.setReadyCondition(dm, metav1.ConditionFalse, "Pending", "Waiting for the Gateway")
		return writeStatus()
	}
	r.setCondition(dm, "GatewayReady", metav1.ConditionTrue, "Ready", "The Gateway on node "+dm.Spec.NodeName+" is ready")

	inv, entry, err := r.reconcileInventory(ctx, dm)
	if err != nil {
		return fail("InventoryUpdateFailed", err)
	}

	if err := r.reconcileHelmRepository(ctx, dm); err != nil {
		return fail("HelmRepositoryFailed", err)
	}
	secretName := tokenSecretName(dm, entry.TokenHash)
	if err := r.reconcileHelmRelease(ctx, dm, secretName); err != nil {
		return fail("HelmReleaseFailed", err)
	}
	// Only after the HelmRelease points at the new Secret, so the old pod keeps its token until rolled.
	if err := r.pruneTokenSecrets(ctx, dm, secretName); err != nil {
		return fail("TokenSecretCleanupFailed", err)
	}

	dm.Status.HelmRepositoryRef = fmt.Sprintf("%s/%s", dm.Namespace, helmRepositoryName(dm))
	dm.Status.HelmReleaseRef = fmt.Sprintf("%s/%s", dm.Namespace, dm.Name)
	dm.Status.DappID = entry.DappID
	dm.Status.NodeName = dm.Spec.NodeName
	r.syncInstalledCondition(ctx, dm)
	r.syncLifecycleConditions(dm, inv, entry)
	return writeStatus()
}

// reconcileDelete removes the dApp's inventory entry, which tells the Gateway to withdraw it,
// and keeps the finalizer until the Gateway confirms by removing the dApp's status entry. On a
// node whose spec is now empty it also waits until no status entry is left. Then it deletes the
// token Secrets and, for the last dApp on the node, the inventory (GC removes the Gateway). GC removes
// the HelmRepository and HelmRelease through their owner references once the finalizer is gone.
func (r *DappManifestReconciler) reconcileDelete(ctx context.Context, dm *cachev1alpha1.DappManifest) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(dm, dappFinalizer) {
		return ctrl.Result{}, nil
	}
	requeueOnConflict := func(err error) (ctrl.Result, error) {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	inv := &cachev1alpha1.DappInventory{}
	getErr := r.Get(ctx, types.NamespacedName{Name: dm.Spec.NodeName}, inv)
	if client.IgnoreNotFound(getErr) != nil {
		return ctrl.Result{}, getErr
	}
	invFound := getErr == nil
	if invFound {
		r.setReadyCondition(dm, metav1.ConditionFalse, "Deleting", "The dApp is being deleted")
		if i := findEntry(inv.Spec.Entries, cachev1alpha1.DappRef{Namespace: dm.Namespace, Name: dm.Name}); i >= 0 {
			// Persist the ID first, so the wait below still finds it once the entry is gone.
			dm.Status.DappID = inv.Spec.Entries[i].DappID
			if err := r.Status().Update(ctx, dm); err != nil {
				return requeueOnConflict(err)
			}
			inv.Spec.Entries = append(inv.Spec.Entries[:i], inv.Spec.Entries[i+1:]...)
			if err := r.Update(ctx, inv); err != nil {
				return requeueOnConflict(err)
			}
		}
		// On an emptying node, every deleting dApp holds until the Gateway has no status entry left,
		// so the last one deletes the inventory and no orphan ID is left behind with the Gateway.
		// The inventory watch triggers the next reconcile in both waits.
		if awaitingWithdraw(inv, dm.Status.DappID) {
			r.setReadyCondition(dm, metav1.ConditionFalse, "WaitingForWithdraw", "Waiting for the Gateway to withdraw the dApp")
			return requeueOnConflict(r.Status().Update(ctx, dm))
		}
		if len(inv.Spec.Entries) == 0 && len(inv.Status.Entries) > 0 {
			r.setReadyCondition(dm, metav1.ConditionFalse, "WaitingForWithdraw", "Waiting for the Gateway to withdraw the remaining dApps on the node")
			return requeueOnConflict(r.Status().Update(ctx, dm))
		}
		r.setCondition(dm, "Advertised", metav1.ConditionFalse, "Withdrawn", "The Gateway withdrew the dApp and removed its status entry")
		if err := r.Status().Update(ctx, dm); err != nil {
			return requeueOnConflict(err)
		}
	}

	if err := r.pruneTokenSecrets(ctx, dm, ""); err != nil {
		return ctrl.Result{}, err
	}
	if invFound && len(inv.Spec.Entries) == 0 {
		// The precondition keeps an entry added since our read from being deleted with the inventory.
		err := r.Delete(ctx, inv, client.Preconditions{ResourceVersion: &inv.ResourceVersion})
		if client.IgnoreNotFound(err) != nil {
			return requeueOnConflict(err)
		}
	}

	controllerutil.RemoveFinalizer(dm, dappFinalizer)
	return requeueOnConflict(client.IgnoreNotFound(r.Update(ctx, dm)))
}

// awaitingWithdraw reports whether the Gateway has not yet confirmed the withdrawal of id, which
// it does by removing the id's status entry. Once another spec entry holds id, the ID was
// withdrawn and reallocated (see allocateDappID), so it is no longer ours.
func awaitingWithdraw(inv *cachev1alpha1.DappInventory, id int32) bool {
	if id == 0 {
		return false
	}
	for _, e := range inv.Spec.Entries {
		if e.DappID == id {
			return false
		}
	}
	return findEntryStatus(inv.Status.Entries, id) != nil
}

// syncLifecycleConditions reports Registered and Advertised from the Gateway's status for the
// current entry generation, and Ready as the aggregate of the four lifecycle conditions.
func (r *DappManifestReconciler) syncLifecycleConditions(dm *cachev1alpha1.DappManifest, inv *cachev1alpha1.DappInventory, entry cachev1alpha1.DappInventoryEntry) {
	es := findEntryStatus(inv.Status.Entries, entry.DappID)
	current := es != nil && es.Generation == entry.Generation
	set := func(condType string, ok bool, message string) {
		if ok {
			r.setCondition(dm, condType, metav1.ConditionTrue, condType, message)
		} else {
			r.setCondition(dm, condType, metav1.ConditionFalse, "Pending", "Waiting for the Gateway")
		}
	}
	set("Registered", current && es.Registered, "E3 Setup accepted with the reserved dApp ID")
	set("Advertised", current && es.Advertised, "The RIC acknowledged the RIC Service Update")

	for _, t := range []string{"GatewayReady", "Installed", "Registered", "Advertised"} {
		if !apimeta.IsStatusConditionTrue(dm.Status.Conditions, t) {
			r.setReadyCondition(dm, metav1.ConditionFalse, "Pending", "Waiting for "+t)
			return
		}
	}
	r.setReadyCondition(dm, metav1.ConditionTrue, "Reconciled", "The dApp is installed, registered and advertised")
}

func (r *DappManifestReconciler) reconcileHelmRepository(ctx context.Context, dappManifest *cachev1alpha1.DappManifest) error {
	helmRepo := &sourcev1.HelmRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      helmRepositoryName(dappManifest),
			Namespace: dappManifest.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, helmRepo, func() error {
		spec := sourcev1.HelmRepositorySpec{
			URL:      dappManifest.Spec.Helm.RepoURL,
			Interval: metav1.Duration{Duration: time.Minute},
		}
		if strings.HasPrefix(dappManifest.Spec.Helm.RepoURL, "oci://") {
			spec.Type = "oci"
		}
		helmRepo.Spec = spec
		return ctrl.SetControllerReference(dappManifest, helmRepo, r.Scheme)
	})
	return err
}

func (r *DappManifestReconciler) reconcileHelmRelease(ctx context.Context, dappManifest *cachev1alpha1.DappManifest, tokenSecret string) error {
	helmRelease := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dappManifest.Name,
			Namespace: dappManifest.Namespace,
		},
	}

	helmRepoName := helmRepositoryName(dappManifest)

	// Chart contract: the chart mounts this Secret at /var/run/orca/e3/token.
	values, err := json.Marshal(map[string]any{"orca": map[string]string{"e3TokenSecret": tokenSecret}})
	if err != nil {
		return err
	}

	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, helmRelease, func() error {
		postRenderers, err := buildSchedulingPostRenderer(dappManifest)
		if err != nil {
			return err
		}
		helmRelease.Spec = helmv2.HelmReleaseSpec{
			Interval: metav1.Duration{Duration: 5 * time.Minute},
			Install: &helmv2.Install{
				CreateNamespace: true,
			},
			Chart: &helmv2.HelmChartTemplate{
				Spec: helmv2.HelmChartTemplateSpec{
					Chart:   dappManifest.Spec.Helm.ChartName,
					Version: dappManifest.Spec.Helm.Version,
					SourceRef: helmv2.CrossNamespaceObjectReference{
						Kind: sourcev1.HelmRepositoryKind,
						Name: helmRepoName,
					},
				},
			},
			PostRenderers: postRenderers,
			Values:        &apiextensionsv1.JSON{Raw: values},
		}
		if dappManifest.Spec.Helm.ReleaseName != "" {
			helmRelease.Spec.ReleaseName = dappManifest.Spec.Helm.ReleaseName
		}
		helmRelease.Spec.TargetNamespace = dappManifest.Spec.Namespace
		return ctrl.SetControllerReference(dappManifest, helmRelease, r.Scheme)
	})
	return err
}

func buildSchedulingPostRenderer(dappManifest *cachev1alpha1.DappManifest) ([]helmv2.PostRenderer, error) {
	podSpec := map[string]interface{}{"nodeName": dappManifest.Spec.NodeName}
	if len(dappManifest.Spec.Tolerations) > 0 {
		podSpec["tolerations"] = dappManifest.Spec.Tolerations
	}

	workloads := []struct{ apiVersion, kind string }{
		{"apps/v1", "Deployment"},
		{"apps/v1", "StatefulSet"},
		{"apps/v1", "DaemonSet"},
		{"batch/v1", "Job"},
	}

	patches := make([]fluxkustomize.Patch, 0, len(workloads))
	for _, w := range workloads {
		obj := map[string]interface{}{
			"apiVersion": w.apiVersion,
			"kind":       w.kind,
			"metadata":   map[string]interface{}{"name": "placeholder"},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"spec": podSpec,
				},
			},
		}
		data, err := sigsyaml.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("marshaling %s patch: %w", w.kind, err)
		}
		patches = append(patches, fluxkustomize.Patch{
			Patch:  string(data),
			Target: &fluxkustomize.Selector{Kind: w.kind},
		})
	}

	return []helmv2.PostRenderer{{Kustomize: &helmv2.Kustomize{Patches: patches}}}, nil
}

func (r *DappManifestReconciler) setReadyCondition(dappManifest *cachev1alpha1.DappManifest, status metav1.ConditionStatus, reason, message string) {
	r.setCondition(dappManifest, "Ready", status, reason, message)
}

func (r *DappManifestReconciler) setCondition(dappManifest *cachev1alpha1.DappManifest, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&dappManifest.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: dappManifest.Generation,
	})
}

func (r *DappManifestReconciler) syncInstalledCondition(ctx context.Context, dappManifest *cachev1alpha1.DappManifest) {
	helmRelease := &helmv2.HelmRelease{}
	if err := r.Get(ctx, types.NamespacedName{Name: dappManifest.Name, Namespace: dappManifest.Namespace}, helmRelease); err != nil {
		r.setCondition(dappManifest, "Installed", metav1.ConditionFalse, "HelmReleaseNotFound", err.Error())
		return
	}

	hrReady := apimeta.FindStatusCondition(helmRelease.Status.Conditions, "Ready")
	if hrReady == nil {
		r.setCondition(dappManifest, "Installed", metav1.ConditionFalse, "Pending", "Waiting for Flux to install the chart")
		return
	}

	r.setCondition(dappManifest, "Installed", hrReady.Status, hrReady.Reason, hrReady.Message)
}

func helmRepositoryName(dappManifest *cachev1alpha1.DappManifest) string {
	return dappManifest.Name + "-helmrepo"
}

const nodeNameIndex = "spec.nodeName"

func nodeNameIndexer(obj client.Object) []string {
	return []string{obj.(*cachev1alpha1.DappManifest).Spec.NodeName}
}

// dappsOnNode lists every DappManifest on the node, including deleting ones with a pending finalizer.
func (r *DappManifestReconciler) dappsOnNode(ctx context.Context, node string) []reconcile.Request {
	var dms cachev1alpha1.DappManifestList
	if err := r.List(ctx, &dms, client.MatchingFields{nodeNameIndex: node}); err != nil {
		logf.FromContext(ctx).Error(err, "listing dappmanifests on node", "node", node)
		return nil
	}
	reqs := make([]reconcile.Request, 0, len(dms.Items))
	for _, dm := range dms.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&dm)})
	}
	return reqs
}

// SetupWithManager sets up the controller with the Manager.
func (r *DappManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &cachev1alpha1.DappManifest{}, nodeNameIndex, nodeNameIndexer); err != nil {
		return err
	}
	gatewayLabel, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchLabels: map[string]string{"app.kubernetes.io/name": "orca-gateway"},
	})
	if err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.DappManifest{}).
		Owns(&sourcev1.HelmRepository{}).
		Owns(&helmv2.HelmRelease{}).
		Watches(&cachev1alpha1.DappInventory{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, o client.Object) []reconcile.Request {
				return r.dappsOnNode(ctx, o.GetName())
			})).
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, o client.Object) []reconcile.Request {
				if owner := metav1.GetControllerOf(o); owner != nil && owner.Kind == "DappInventory" {
					return r.dappsOnNode(ctx, owner.Name)
				}
				return nil
			}), builder.WithPredicates(gatewayLabel)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Named("dappmanifest").
		Complete(r)
}
