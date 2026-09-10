#!/usr/bin/env bash
# Runs the full testbed provisioning sequence, in order. Each step is
# idempotent (safe to re-run / resume after a partial failure).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

for step in 01-create-cluster.sh 02-install-near-rt-ric.sh 03-install-fluxcd.sh \
            04-install-orca.sh 05-install-non-rt-ric.sh 06-deploy-sample-dapp.sh; do
  echo "=== $step ===" >&2
  ./"$step"
done

echo "=== Testbed fully provisioned. ===" >&2
