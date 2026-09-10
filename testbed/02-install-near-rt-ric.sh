#!/usr/bin/env bash
# Deploys the Near-RT RIC platform (ricplt/ricinfra) via the official
# ric-dep installer, pinned to the commit the live environment was built
# from, with the two local patches that were needed to get it working on
# a current Helm 3 / kind setup (see patches/ric-dep-helm3-only.patch).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
source ./lib.sh
require_context

mkdir -p "$BUILD_DIR"
DEP_DIR="$BUILD_DIR/it-dep"

if [ ! -d "$DEP_DIR" ]; then
  log "Cloning it/dep @ $IT_DEP_COMMIT ..."
  git clone https://gerrit.o-ran-sc.org/r/it/dep "$DEP_DIR"
  git -C "$DEP_DIR" checkout "$IT_DEP_COMMIT"
  log "Fetching ric-dep submodule @ $RIC_DEP_COMMIT ..."
  git -C "$DEP_DIR" submodule update --init ric-dep
  git -C "$DEP_DIR/ric-dep" checkout "$RIC_DEP_COMMIT"

  log "Applying local ric-dep patches (Helm3-only, unsigned servecm plugin)..."
  git -C "$DEP_DIR/ric-dep" apply --whitespace=fix "$TESTBED_DIR/patches/ric-dep-helm3-only.patch"
else
  log "$DEP_DIR already present, reusing."
fi

RECIPE="$DEP_DIR/RECIPE_EXAMPLE/PLATFORM/$RIC_DEP_RECIPE"
[ -f "$RECIPE" ] || { echo "ERROR: recipe not found at $RECIPE" >&2; exit 1; }

export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"
kubectl config use-context "$KIND_CONTEXT" >/dev/null

cd "$DEP_DIR/ric-dep/bin"

log "Installing common Helm templates (chartmuseum + ric-common)..."
# NOTE: the original manual setup ran this (and `install` below) with sudo.
# It works without sudo against this user's kubeconfig/context; if you hit
# permission errors related to the local chartmuseum server, retry with
# `sudo -E ./install_common_templates_to_helm.sh` to preserve HOME/KUBECONFIG.
./install_common_templates_to_helm.sh

log "Installing Near-RT RIC platform from $RECIPE ..."
./install -f "$RECIPE"

log "Near-RT RIC install triggered. Waiting for ricplt pods to become Ready (up to 5m)..."
kubectl --context "$KIND_CONTEXT" wait --for=condition=Ready pods --all -n ricplt --timeout=300s || \
  log "WARNING: not all ricplt pods reported Ready within 5m — check 'kubectl get pods -n ricplt'."

kubectl --context "$KIND_CONTEXT" get pods -n ricplt
kubectl --context "$KIND_CONTEXT" get pods -n ricinfra
