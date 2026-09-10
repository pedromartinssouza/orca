#!/usr/bin/env bash
# Deletes the Kind cluster entirely (all namespaces/releases go with it).
# Does NOT remove .build/ (cached clones/charts) so the next deploy-all.sh
# is fast -- pass --clean to also wipe .build/.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  log "Deleting Kind cluster '$CLUSTER_NAME'..."
  kind delete cluster --name "$CLUSTER_NAME"
else
  log "Cluster '$CLUSTER_NAME' not found, nothing to delete."
fi

if [ "${1:-}" = "--clean" ]; then
  log "Removing cached build artifacts ($BUILD_DIR)..."
  rm -rf "$BUILD_DIR"
fi
