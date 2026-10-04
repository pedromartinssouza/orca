#!/usr/bin/env bash
# RTO (Recovery Time Objective): measures the full recovery time from
# HelmRelease deletion to all app pods being Ready again.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/setup.sh"
check_prerequisites

echo "Deleting HelmRelease '$DAPP_NAME' in '$OPERATOR_NS' to simulate failure..."

T1_NS=$(date +%s%N)

kubectl delete helmrelease "$DAPP_NAME" -n "$OPERATOR_NS"

echo "Waiting for HelmRelease to be recreated and chart fully installed..."
kubectl wait helmrelease "$DAPP_NAME" -n "$OPERATOR_NS" --for=create --timeout=300s
kubectl wait helmrelease "$DAPP_NAME" -n "$OPERATOR_NS" --for=condition=Ready --timeout=300s

T2_NS=$(date +%s%N)
RTO_MS=$(( (T2_NS - T1_NS) / 1000000 ))

echo ""
echo "RTO: ${RTO_MS}ms ($(( RTO_MS / 1000 ))s)"
