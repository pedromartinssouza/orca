#!/usr/bin/env bash
# Shared config for testbed/*.sh. Source this, don't execute it.
set -euo pipefail

export CLUSTER_NAME="${CLUSTER_NAME:-multi-node-cluster}"
export KIND_CONTEXT="kind-${CLUSTER_NAME}"

# Pinned so `it/dep` always has the RECIPE_EXAMPLE/PLATFORM layout this
# testbed was validated against, and `ric-dep` is the exact commit the
# live environment (verified 2026-09-09/10) was built from.
export IT_DEP_COMMIT="8f797d67cc00e1130b60be582393cbaabebd93fe"
export RIC_DEP_COMMIT="348562bc2adad5c9e6f8a114db2cbfc469be710a"
export RIC_DEP_RECIPE="example_recipe_latest_stable.yaml"

export ORCA_CHART_VERSION="${ORCA_CHART_VERSION:-0.5.0}"
export ORCA_NAMESPACE="orca-system"

export NONRTRIC_NAMESPACE="nonrtric"
export NONRTRIC_COMMON_VERSION="2.0.0"
export PMS_VERSION="2.0.0"
export NEXUS_HELM_REPO="https://nexus3.o-ran-sc.org/repository/helm.release/"

TESTBED_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export TESTBED_DIR
export BUILD_DIR="${TESTBED_DIR}/.build"

log() { echo "[testbed] $*" >&2; }

require_context() {
  if ! kubectl config get-contexts "$KIND_CONTEXT" &>/dev/null; then
    echo "ERROR: context $KIND_CONTEXT not found. Run 01-create-cluster.sh first." >&2
    exit 1
  fi
}
