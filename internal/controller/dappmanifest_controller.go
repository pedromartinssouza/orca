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
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	fluxkustomize "github.com/fluxcd/pkg/apis/kustomize"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	sigsyaml "sigs.k8s.io/yaml"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

// DappManifestReconciler reconciles a DappManifest object
type DappManifestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cache.orca.com,resources=dappmanifests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cache.orca.com,resources=dappmanifests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cache.orca.com,resources=dappmanifests/finalizers,verbs=update
// +kubebuilder:rbac:groups=source.toolkit.fluxcd.io,resources=helmrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=helm.toolkit.fluxcd.io,resources=helmreleases,verbs=get;list;watch;create;update;patch;delete

func (r *DappManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	dappManifest := &cachev1alpha1.DappManifest{}
	if err := r.Get(ctx, req.NamespacedName, dappManifest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log.Info("reconciling dappmanifest", "name", req.NamespacedName)

	if err := r.reconcileHelmRepository(ctx, dappManifest); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to reconcile HelmRepository")
		r.setReadyCondition(dappManifest, metav1.ConditionFalse, "HelmRepositoryFailed", err.Error())
		_ = r.Status().Update(ctx, dappManifest)
		return ctrl.Result{}, err
	}

	if err := r.reconcileHelmRelease(ctx, dappManifest); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "failed to reconcile HelmRelease")
		r.setReadyCondition(dappManifest, metav1.ConditionFalse, "HelmReleaseFailed", err.Error())
		_ = r.Status().Update(ctx, dappManifest)
		return ctrl.Result{}, err
	}

	helmRepoName := helmRepositoryName(dappManifest)
	r.setReadyCondition(dappManifest, metav1.ConditionTrue, "Reconciled", "HelmRepository and HelmRelease are configured")
	dappManifest.Status.HelmRepositoryRef = fmt.Sprintf("%s/%s", dappManifest.Namespace, helmRepoName)
	dappManifest.Status.HelmReleaseRef = fmt.Sprintf("%s/%s", dappManifest.Namespace, dappManifest.Name)

	r.syncInstalledCondition(ctx, dappManifest)

	if err := r.Status().Update(ctx, dappManifest); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, err
	}

	installed := apimeta.FindStatusCondition(dappManifest.Status.Conditions, "Installed")
	if installed == nil || installed.Status != metav1.ConditionTrue {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
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

func (r *DappManifestReconciler) reconcileHelmRelease(ctx context.Context, dappManifest *cachev1alpha1.DappManifest) error {
	helmRelease := &helmv2.HelmRelease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dappManifest.Name,
			Namespace: dappManifest.Namespace,
		},
	}

	helmRepoName := helmRepositoryName(dappManifest)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, helmRelease, func() error {
		postRenderers, err := buildSchedulingPostRenderer(dappManifest)
		if err != nil {
			return err
		}
		helmRelease.Spec = helmv2.HelmReleaseSpec{
			Interval: metav1.Duration{Duration: 5 * time.Minute},
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
		}
		if dappManifest.Spec.Helm.ReleaseName != "" {
			helmRelease.Spec.ReleaseName = dappManifest.Spec.Helm.ReleaseName
		}
		if dappManifest.Spec.Namespace != "" {
			helmRelease.Spec.TargetNamespace = dappManifest.Spec.Namespace
		}
		return ctrl.SetControllerReference(dappManifest, helmRelease, r.Scheme)
	})
	return err
}

func buildSchedulingPostRenderer(dappManifest *cachev1alpha1.DappManifest) ([]helmv2.PostRenderer, error) {
	if len(dappManifest.Spec.NodeSelector) == 0 && len(dappManifest.Spec.Tolerations) == 0 {
		return nil, nil
	}

	podSpec := map[string]interface{}{}
	if len(dappManifest.Spec.NodeSelector) > 0 {
		podSpec["nodeSelector"] = dappManifest.Spec.NodeSelector
	}
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

// SetupWithManager sets up the controller with the Manager.
func (r *DappManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cachev1alpha1.DappManifest{}).
		Owns(&sourcev1.HelmRepository{}).
		Owns(&helmv2.HelmRelease{}).
		Named("dappmanifest").
		Complete(r)
}
