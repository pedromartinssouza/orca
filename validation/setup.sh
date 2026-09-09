#!/usr/bin/env bash
# Sourced by validation scripts. Exports shared config and checks prerequisites.
set -euo pipefail

export OPERATOR_NS="${OPERATOR_NS:-orca-system}"
export TARGET_NS="${TARGET_NS:-dapp-sample-system}"
export DAPP_NAME="${DAPP_NAME:-dapp-sample}"
export APP_LABEL="app=${DAPP_NAME}"

check_prerequisites() {
  if ! kubectl cluster-info &>/dev/null; then
    echo "ERROR: kubectl cannot reach the cluster." >&2
    exit 1
  fi

  if ! kubectl get dappmanifest "$DAPP_NAME" -n "$OPERATOR_NS" &>/dev/null; then
    echo "ERROR: DappManifest '$DAPP_NAME' not found in namespace '$OPERATOR_NS'." >&2
    echo "Apply it first: kubectl apply -f validation/dapp-sample.yaml" >&2
    exit 1
  fi

  READY=$(kubectl get dappmanifest "$DAPP_NAME" -n "$OPERATOR_NS" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
  if [ "$READY" != "True" ]; then
    echo "ERROR: DappManifest '$DAPP_NAME' is not Ready (status: ${READY:-unknown})." >&2
    exit 1
  fi

  if ! kubectl get pods -n "$TARGET_NS" -l "$APP_LABEL" --field-selector=status.phase=Running \
      -o name 2>/dev/null | grep -q .; then
    echo "ERROR: No Running pods found in '$TARGET_NS' with label '$APP_LABEL'." >&2
    exit 1
  fi

  echo "Baseline OK — DappManifest is Ready and pods are running."
}
