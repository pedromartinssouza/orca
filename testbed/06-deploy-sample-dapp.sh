#!/usr/bin/env bash
# Applies the sample DappManifest CR, exercising the full ORCA -> FluxCD ->
# HelmRelease -> dApp pod path end to end.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh
require_context

kubectl config use-context "$KIND_CONTEXT" >/dev/null

# Workaround for https://github.com/pedromartinssouza/orca/pull/5: ORCA
# 0.5.0's generated HelmRelease doesn't set install.createNamespace, so
# FluxCD's helm-controller never creates the target namespace and the
# release permanently stalls after one failed attempt (no auto-retry) if
# it doesn't already exist. Pre-create it here so this script works
# against the currently-pinned ORCA_CHART_VERSION regardless of whether
# that fix has shipped yet. Safe to drop once ORCA_CHART_VERSION in
# lib.sh is bumped past the fix.
DAPP_TARGET_NS=$(awk '/^spec:/{f=1} f && /^  namespace:/{print $2; exit}' ../validation/dapp-sample.yaml)
kubectl --context "$KIND_CONTEXT" create namespace "$DAPP_TARGET_NS" \
  --dry-run=client -o yaml | kubectl --context "$KIND_CONTEXT" apply -f -

kubectl --context "$KIND_CONTEXT" apply -f ../validation/dapp-sample.yaml

log "Waiting for dapp-sample DappManifest to become Ready (up to 3m)..."
for _ in $(seq 1 36); do
  READY=$(kubectl --context "$KIND_CONTEXT" get dappmanifest dapp-sample -n "$ORCA_NAMESPACE" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
  [ "$READY" = "True" ] && break
  sleep 5
done

kubectl --context "$KIND_CONTEXT" get dappmanifest dapp-sample -n "$ORCA_NAMESPACE"
kubectl --context "$KIND_CONTEXT" get pods -n dapp-sample-system
