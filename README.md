<p align="center">
  <img src="docs/assets/logo.jpg" width="180" alt="orca logo" />
</p>

# orca

ORCA (Operator for RAN-native Cloud Applications) is a Kubernetes Operator that manages the lifecycle of **dApps** — microservices deployed inside an O-RAN O-Cloud, co-located with a vO-CU or vO-DU for sub-10ms control loops.

Today, standing up a dApp means hand-wiring Helm releases, scheduling constraints, and status checks yourself. ORCA replaces that with one declarative object: a `DappManifest` CR. ORCA turns it into a `HelmRepository` + `HelmRelease` (reconciled by FluxCD, not Helm directly), propagates scheduling to the rendered pods, and aggregates status back onto the CR as the single thing you watch.

This is part of a UNISINOS master's dissertation on dApp Lifecycle Management in O-RAN. A planned second component, the **Gateway RAN Function**, will bridge xApp↔dApp communication over E2SM-DAPP — that part isn't built yet; this repo is the Operator.

## What a DappManifest looks like

```yaml
apiVersion: cache.orca.com/v1alpha1
kind: DappManifest
metadata:
  name: my-dapp
spec:
  namespace: my-dapp-system
  helm:
    chartName: my-dapp-chart
    version: ">=1.0.0"
    repoURL: oci://ghcr.io/you/charts
  nodeName: o-du-worker-2      # pin to one exact node...
  nodeSelector:                # ...or target any node of a type
    type: o-du
  tolerations:
  - key: dedicated
    operator: Equal
    value: o-du
    effect: NoSchedule
```

Apply it, and ORCA takes care of the rest:

```sh
kubectl apply -f my-dapp.yaml
kubectl get dappmanifest my-dapp   # READY=True, INSTALLED=True once live
```

## Installing ORCA

ORCA ships as a Helm chart, published to GHCR on every merge to `main`:

```sh
helm install orca oci://ghcr.io/pedromartinssouza/charts/orca --version 0.5.0 \
  --namespace orca-system --create-namespace
```

Check [pedromartinssouza's GHCR packages](https://github.com/pedromartinssouza?tab=packages) for the latest chart version. ORCA also needs [FluxCD's HelmController and SourceController](https://fluxcd.io/flux/installation/) running in the cluster — it creates `HelmRepository`/`HelmRelease` objects, Flux does the actual installing.

## Local development

Prerequisites: Go 1.24+, Docker, `kubectl`, access to a cluster.

```sh
make install                              # CRDs
make run                                  # run the controller from your host
# or, to run it in-cluster:
make docker-build docker-push IMG=<registry>/orca:tag
make deploy IMG=<registry>/orca:tag
```

```sh
make test      # unit + envtest suite
make test-e2e  # spins up its own Kind cluster
```

`make help` lists every other target.

## Testbed: full environment setup

`testbed/` provisions the complete environment ORCA is validated
against: a 4-node Kind cluster, a real Near-RT RIC (via the official
`ric-dep` installer), FluxCD, ORCA itself, a minimal Non-RT RIC (Policy
Management Service, talking real A1 to the Near-RT RIC), and a sample
dApp exercising the full LCM path. It's built to be torn down and
rebuilt from nothing as many times as needed, and has been verified end
to end by doing exactly that. See [`testbed/README.md`](testbed/README.md)
for the full rationale (why the Non-RT RIC slice is PMS-only, provenance
of the pinned versions/patches, and issues found and fixed along the way).

**Additional prerequisites** beyond the ones above: [`kind`](https://kind.sigs.k8s.io/),
[`helm`](https://helm.sh/), and the [`flux` CLI](https://fluxcd.io/flux/cmd/).

### Step by step

1. **Create the cluster:**
   ```sh
   cd testbed
   ./01-create-cluster.sh
   ```
   Creates the 4-node Kind cluster (`kind-setup/kind-config.yaml`):
   control-plane, a `components` worker, and tainted `o-du`/`o-cu`
   workers for scheduling dApps onto.

2. **Install the Near-RT RIC platform:**
   ```sh
   ./02-install-near-rt-ric.sh
   ```
   Clones `it/dep` + the `ric-dep` submodule at pinned commits, applies
   the two local patches in `patches/ric-dep-helm3-only.patch`, and runs
   the official installer. Takes a few minutes (image pulls); waits for
   all `ricplt`/`ricinfra` pods to become Ready.

3. **Install FluxCD:**
   ```sh
   ./03-install-fluxcd.sh
   ```
   Plain `flux install` — no Git source. ORCA drives `HelmRelease`/
   `HelmRepository` objects directly; Flux just reconciles them.

4. **Install ORCA:**
   ```sh
   ./04-install-orca.sh
   ```
   Installs the operator from its published OCI Helm chart
   (`oci://ghcr.io/pedromartinssouza/charts/orca`) into `orca-system`.

5. **Install the Non-RT RIC (PMS):**
   ```sh
   ./05-install-non-rt-ric.sh
   ```
   Deploys `nonrtric-common` + `policymanagementservice` into a new
   `nonrtric` namespace, configured via `values/pms-values-override.yaml`
   to talk directly to the Near-RT RIC's `a1mediator` (no ONAP).

6. **Deploy the sample dApp:**
   ```sh
   ./06-deploy-sample-dapp.sh
   ```
   Applies a sample `DappManifest` CR and waits for it to reach
   `Ready`/`Installed`, exercising the full ORCA → FluxCD → HelmRelease →
   pod path.

Or run all six in order in one go:

```sh
cd testbed
./deploy-all.sh
```

### Verifying it worked

```sh
for ns in ricplt ricinfra orca-system nonrtric dapp-sample-system; do
  kubectl get pods -n "$ns"
done
kubectl get dappmanifest dapp-sample -n orca-system      # READY=True, INSTALLED=True
kubectl exec -n nonrtric policymanagementservice-0 -- \
  wget -qO- http://localhost:8081/a1-policy/v2/rics       # near-rt-ric-1, state AVAILABLE
```

### Tearing down

```sh
cd testbed
./teardown.sh            # deletes the Kind cluster
./teardown.sh --clean     # also wipes cached clones/charts in testbed/.build/
```

## Releases

Pushing to `main` with a commit message starting `feat:`, `fix:`, or
`breaking change:` triggers `.github/workflows/publish.yml`: it bumps
`charts/orca/Chart.yaml`, builds and pushes the manager image and both
Helm charts (`orca` and the sample `dapp-sample-app`) to
`ghcr.io/pedromartinssouza`. Any other commit message skips the bump
(no release).

## Contributing

This is a research prototype backing a master's dissertation, not
accepting outside contributions right now. Issues are welcome.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
