#!/usr/bin/env bash
# TTD (Time to Detect): measures how long the operator takes to notice a
# deleted HelmRelease and start reconciling.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/setup.sh"
check_prerequisites

OPERATOR_POD=$(kubectl get pod -n "$OPERATOR_NS" -l app.kubernetes.io/name=orca \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -z "$OPERATOR_POD" ]; then
  OPERATOR_POD=$(kubectl get pod -n "$OPERATOR_NS" -l control-plane=controller-manager \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
fi
if [ -z "$OPERATOR_POD" ]; then
  echo "ERROR: No operator pod found in '$OPERATOR_NS'. Is the operator running as a pod (not 'make run')?" >&2
  exit 1
fi

echo "Using operator pod: $OPERATOR_POD"
echo "Deleting HelmRelease '$DAPP_NAME' in '$OPERATOR_NS'..."

T1_NS=$(date +%s%N)
T1_RFC3339=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

kubectl delete helmrelease "$DAPP_NAME" -n "$OPERATOR_NS"

echo "Waiting for operator to log reconcile start..."
while true; do
  LOG_LINE=$(kubectl logs "$OPERATOR_POD" -n "$OPERATOR_NS" \
    --since-time="$T1_RFC3339" 2>/dev/null \
    | grep "reconciling dapp" | head -1)
  if [ -n "$LOG_LINE" ]; then
    break
  fi
  sleep 0.1
done

T2_NS=$(date +%s%N)
TTD_MS=$(( (T2_NS - T1_NS) / 1000000 ))

echo ""
echo "TTD: ${TTD_MS}ms"
echo "(Log line: $LOG_LINE)"
