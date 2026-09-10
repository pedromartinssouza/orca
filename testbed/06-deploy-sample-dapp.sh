#!/usr/bin/env bash
# Applies the sample DappManifest CR, exercising the full ORCA -> FluxCD ->
# HelmRelease -> dApp pod path end to end.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh
require_context

kubectl config use-context "$KIND_CONTEXT" >/dev/null
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
