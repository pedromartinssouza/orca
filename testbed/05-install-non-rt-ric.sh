#!/usr/bin/env bash
# Deploys the minimal Non-RT RIC slice: Policy Management Service (PMS),
# the only component that does real work for this project's architecture
# (it's the A1 policy manager that talks to the Near-RT RIC's a1mediator).
#
# Deliberately skips: ONAP/SMO (PMS needs no ONAP component once the SDNC
# controller and DMaaP streaming are disabled, see values/pms-values-override.yaml),
# rApp Manager (requires ONAP ACM as its only deployment backend, and is
# unrelated to ORCA -- see testbed/README.md), A1 Simulator (we have a real
# Near-RT RIC), Control Panel / SME / DME (not needed unless/until real
# rApps or a UI are required).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh
require_context

kubectl config use-context "$KIND_CONTEXT" >/dev/null
kubectl --context "$KIND_CONTEXT" create namespace "$NONRTRIC_NAMESPACE" \
  --dry-run=client -o yaml | kubectl --context "$KIND_CONTEXT" apply -f -

mkdir -p "$BUILD_DIR/nonrtric-charts"
cd "$BUILD_DIR/nonrtric-charts"

fetch_chart() {
  local name="$1" version="$2"
  if [ ! -f "${name}-${version}.tgz" ]; then
    log "Fetching ${name} ${version} from Nexus..."
    curl -sf -o "${name}-${version}.tgz" "${NEXUS_HELM_REPO}${name}-${version}.tgz"
  fi
}

fetch_chart nonrtric-common "$NONRTRIC_COMMON_VERSION"
fetch_chart policymanagementservice "$PMS_VERSION"

rm -rf policymanagementservice
tar -xzf "policymanagementservice-${PMS_VERSION}.tgz"
# nonrtric-common is a chart-local dependency (Chart.yaml declares
# repository: '@local', a chartmuseum alias we don't have) -- vendor it
# directly into charts/ instead of relying on repo resolution.
mkdir -p policymanagementservice/charts
cp "nonrtric-common-${NONRTRIC_COMMON_VERSION}.tgz" \
   "policymanagementservice/charts/nonrtric-common-${NONRTRIC_COMMON_VERSION}.tgz"

if helm --kube-context "$KIND_CONTEXT" status pms -n "$NONRTRIC_NAMESPACE" &>/dev/null; then
  log "pms release already present in $NONRTRIC_NAMESPACE, upgrading in place."
  helm --kube-context "$KIND_CONTEXT" upgrade pms ./policymanagementservice \
    --namespace "$NONRTRIC_NAMESPACE" \
    -f "$TESTBED_DIR/values/pms-values-override.yaml"
  # A ConfigMap-only change doesn't trigger a StatefulSet pod restart on its
  # own -- force one so the running process actually picks up the new
  # application_configuration.json / application.yml.
  log "Restarting pms pod to pick up config changes..."
  kubectl --context "$KIND_CONTEXT" delete pod policymanagementservice-0 -n "$NONRTRIC_NAMESPACE" --ignore-not-found
else
  log "Installing pms into $NONRTRIC_NAMESPACE ..."
  helm --kube-context "$KIND_CONTEXT" install pms ./policymanagementservice \
    --namespace "$NONRTRIC_NAMESPACE" \
    -f "$TESTBED_DIR/values/pms-values-override.yaml"
fi

kubectl --context "$KIND_CONTEXT" wait --for=condition=Ready pod \
  -l app.kubernetes.io/name=policymanagementservice -n "$NONRTRIC_NAMESPACE" --timeout=180s || \
  log "WARNING: pms pod not Ready within 3m — check 'kubectl get pods -n $NONRTRIC_NAMESPACE' and 'kubectl logs'."
kubectl --context "$KIND_CONTEXT" get pods -n "$NONRTRIC_NAMESPACE"
