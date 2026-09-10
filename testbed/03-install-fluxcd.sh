#!/usr/bin/env bash
# Bootstraps FluxCD controllers (no Git source — ORCA drives HelmRelease/
# HelmRepository objects directly, Flux just reconciles them).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh
require_context

kubectl config use-context "$KIND_CONTEXT" >/dev/null

if kubectl --context "$KIND_CONTEXT" get ns flux-system &>/dev/null; then
  log "flux-system namespace already present, skipping 'flux install'."
else
  log "Running 'flux install'..."
  flux install
fi

kubectl --context "$KIND_CONTEXT" wait --for=condition=Available deployment --all -n flux-system --timeout=180s
kubectl --context "$KIND_CONTEXT" get pods -n flux-system
