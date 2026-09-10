#!/usr/bin/env bash
# Creates the 4-node Kind cluster: control-plane(main) + worker(components)
# + worker(o-du, tainted) + worker(o-cu, tainted). Node labels/taints are
# baked into kind-setup/kind-config.yaml via kubeadm patches.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "Cluster '$CLUSTER_NAME' already exists, skipping creation."
else
  log "Creating Kind cluster '$CLUSTER_NAME'..."
  kind create cluster --config ../kind-setup/kind-config.yaml --name "$CLUSTER_NAME"
fi

kubectl --context "$KIND_CONTEXT" get nodes -o custom-columns=NAME:.metadata.name,TYPE:'.metadata.labels.type'
log "Cluster ready."
