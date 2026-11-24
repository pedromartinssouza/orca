package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

// gatewayServiceAccount must match the ServiceAccount shipped in config/rbac and charts/orca.
const gatewayServiceAccount = "orca-gateway"

func gatewayName(node string) string { return "orca-gateway-" + node }

// ensureGateway creates or updates the per-node Gateway Deployment, owned by inv
// so that deleting the inventory garbage-collects it. ready is true once a replica is available.
func (r *DappManifestReconciler) ensureGateway(ctx context.Context, inv *cachev1alpha1.DappInventory) (bool, error) {
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      gatewayName(inv.Name),
		Namespace: r.GatewayNamespace,
	}}
	labels := map[string]string{"app.kubernetes.io/name": "orca-gateway"}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Labels = labels
		dep.Spec.Replicas = ptr.To(int32(1))
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{
			"app.kubernetes.io/name":   "orca-gateway",
			"orca.cache.orca.com/node": inv.Name,
		}}
		dep.Spec.Template.Labels = map[string]string{
			"app.kubernetes.io/name":   "orca-gateway",
			"orca.cache.orca.com/node": inv.Name,
		}
		dep.Spec.Template.Spec = corev1.PodSpec{
			NodeName:           inv.Name,
			ServiceAccountName: gatewayServiceAccount,
			Containers: []corev1.Container{{
				Name:  "gateway",
				Image: r.GatewayImage,
				Env: []corev1.EnvVar{{
					Name:      "NODE_NAME",
					ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}},
				}},
			}},
		}
		return ctrl.SetControllerReference(inv, dep, r.Scheme)
	})
	if err != nil {
		return false, err
	}
	return dep.Status.AvailableReplicas >= 1, nil
}
