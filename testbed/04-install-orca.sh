#!/usr/bin/env bash
# Installs ORCA from its published OCI Helm chart.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh
require_context

kubectl config use-context "$KIND_CONTEXT" >/dev/null

if helm --kube-context "$KIND_CONTEXT" status orca -n "$ORCA_NAMESPACE" &>/dev/null; then
  log "orca release already present in $ORCA_NAMESPACE, skipping install."
else
  log "Installing orca $ORCA_CHART_VERSION into $ORCA_NAMESPACE ..."
  helm --kube-context "$KIND_CONTEXT" install orca \
    "oci://ghcr.io/pedromartinssouza/charts/orca" \
    --version "$ORCA_CHART_VERSION" \
    --namespace "$ORCA_NAMESPACE" \
    --create-namespace
fi

kubectl --context "$KIND_CONTEXT" wait --for=condition=Available deployment/orca -n "$ORCA_NAMESPACE" --timeout=180s
kubectl --context "$KIND_CONTEXT" get pods -n "$ORCA_NAMESPACE"
