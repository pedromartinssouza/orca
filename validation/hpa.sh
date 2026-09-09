#!/usr/bin/env bash
# HPA (Horizontal Pod Autoscaler): measures how long it takes for the cluster
# to scale out the app Deployment under CPU load.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/setup.sh"
check_prerequisites

METRICS_SERVER_NS="kube-system"
METRICS_SERVER_DEPLOY="metrics-server"

# Install metrics-server if absent
if ! kubectl get deployment "$METRICS_SERVER_DEPLOY" -n "$METRICS_SERVER_NS" &>/dev/null; then
  echo "Installing metrics-server..."
  kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
fi

# Patch for kind (needs --kubelet-insecure-tls)
if ! kubectl get deployment "$METRICS_SERVER_DEPLOY" -n "$METRICS_SERVER_NS" \
    -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q "insecure-tls"; then
  echo "Patching metrics-server for kind..."
  kubectl patch deployment "$METRICS_SERVER_DEPLOY" -n "$METRICS_SERVER_NS" \
    --type='json' \
    -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
fi

echo "Waiting for metrics-server to be Ready..."
kubectl rollout status deployment/"$METRICS_SERVER_DEPLOY" -n "$METRICS_SERVER_NS" --timeout=120s

# Apply HPA
echo "Applying HPA..."
kubectl autoscale deployment "$DAPP_NAME" \
  -n "$TARGET_NS" \
  --cpu-percent=50 \
  --min=1 \
  --max=5 2>/dev/null || true

# Start load generator
echo "Starting load generator..."
SVC_URL="http://${DAPP_NAME}.${TARGET_NS}.svc.cluster.local:8080"
kubectl run load-gen \
  --image=busybox \
  -n "$TARGET_NS" \
  --restart=Never \
  -- sh -c "while true; do wget -qO- ${SVC_URL}; done" 2>/dev/null || true

T1_NS=$(date +%s%N)
echo "Load generator running. Waiting for scale-out..."

while true; do
  REPLICAS=$(kubectl get hpa "$DAPP_NAME" -n "$TARGET_NS" \
    -o jsonpath='{.status.currentReplicas}' 2>/dev/null || echo "0")
  if [ "${REPLICAS:-0}" -gt 1 ] 2>/dev/null; then
    break
  fi
  sleep 5
done

T2_NS=$(date +%s%N)
HPA_MS=$(( (T2_NS - T1_NS) / 1000000 ))

echo ""
echo "HPA scale-out: ${HPA_MS}ms ($(( HPA_MS / 1000 ))s)"

# Cleanup
echo "Cleaning up..."
kubectl delete pod load-gen -n "$TARGET_NS" --ignore-not-found
kubectl delete hpa "$DAPP_NAME" -n "$TARGET_NS" --ignore-not-found
