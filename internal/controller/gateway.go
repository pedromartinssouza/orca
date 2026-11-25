package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

const (
	// gatewayServiceAccount must match the ServiceAccount shipped in config/rbac and charts/orca.
	gatewayServiceAccount = "orca-gateway"
	// GatewayNameLabel marks the Gateway Deployments and pods; the manager caches only these Deployments.
	GatewayNameLabel, GatewayNameValue = "app.kubernetes.io/name", "orca-gateway"
	// nodeHashLabel keeps the Gateway selector stable across inventory recreations and within
	// the 63 character label value limit.
	nodeHashLabel    = "orca.cache.orca.com/node-hash"
	gatewayContainer = "gateway"
)

func gatewayName(node string) string { return "orca-gateway-" + node }

func nodeHash(node string) string {
	h := sha256.Sum256([]byte(node))
	return hex.EncodeToString(h[:])[:16]
}

// ensureGateway creates or updates the per-node Gateway Deployment, owned by inv
// so that deleting the inventory garbage-collects it. ready is true once a replica is available.
// It sets only the fields ORCA owns, so an unchanged Deployment is not updated.
func (r *DappManifestReconciler) ensureGateway(ctx context.Context, inv *cachev1alpha1.DappInventory) (bool, error) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      gatewayName(inv.Name),
		Namespace: r.GatewayNamespace,
	}}
	podLabels := map[string]string{GatewayNameLabel: GatewayNameValue, nodeHashLabel: nodeHash(inv.Name)}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		if dep.Labels == nil {
			dep.Labels = map[string]string{}
		}
		dep.Labels[GatewayNameLabel] = GatewayNameValue
		dep.Spec.Replicas = ptr.To(int32(1))
		// Recreate keeps the Gateway a singleton per node during image changes.
		dep.Spec.Strategy.Type = appsv1.RecreateDeploymentStrategyType
		if dep.CreationTimestamp.IsZero() {
			// The selector is immutable; it does not depend on the inventory, so it never needs to change.
			dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: podLabels}
		}
		if dep.Spec.Template.Labels == nil {
			dep.Spec.Template.Labels = map[string]string{}
		}
		for k, v := range podLabels {
			dep.Spec.Template.Labels[k] = v
		}
		pod := &dep.Spec.Template.Spec
		pod.NodeName = inv.Name
		pod.ServiceAccountName = gatewayServiceAccount

		i := 0
		for i < len(pod.Containers) && pod.Containers[i].Name != gatewayContainer {
			i++
		}
		if i == len(pod.Containers) {
			pod.Containers = append(pod.Containers, corev1.Container{Name: gatewayContainer})
		}
		c := &pod.Containers[i]
		c.Image = r.GatewayImage
		nodeEnv := corev1.EnvVar{
			Name:      "NODE_NAME",
			ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "spec.nodeName"}},
		}
		j := 0
		for j < len(c.Env) && c.Env[j].Name != nodeEnv.Name {
			j++
		}
		if j == len(c.Env) {
			c.Env = append(c.Env, nodeEnv)
		} else {
			c.Env[j] = nodeEnv
		}
		return ctrl.SetControllerReference(inv, dep, r.Scheme)
	})
	if err != nil {
		return false, err
	}
	return dep.Status.ObservedGeneration >= dep.Generation &&
		dep.Status.UpdatedReplicas >= 1 && dep.Status.AvailableReplicas >= 1, nil
}
