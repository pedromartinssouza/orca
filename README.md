<p align="center">
  <img src="docs/assets/logo.jpg" width="180" alt="orca logo" />
</p>

# orca
// TODO(user): Add simple overview of use/purpose

## Description
// TODO(user): An in-depth paragraph about your project and overview of use

## Getting Started

### Prerequisites
- go version v1.24.0+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster
**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/orca:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/orca:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Testbed: Full Environment Setup

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

## Project Distribution

Following the options to release and provide this solution to the users.

### By providing a bundle with all YAML files

1. Build the installer for the image built and published in the registry:

```sh
make build-installer IMG=<some-registry>/orca:tag
```

**NOTE:** The makefile target mentioned above generates an 'install.yaml'
file in the dist directory. This file contains all the resources built
with Kustomize, which are necessary to install this project without its
dependencies.

2. Using the installer

Users can just run 'kubectl apply -f <URL for YAML BUNDLE>' to install
the project, i.e.:

```sh
kubectl apply -f https://raw.githubusercontent.com/<org>/orca/<tag or branch>/dist/install.yaml
```

### By providing a Helm Chart

1. Build the chart using the optional helm plugin

```sh
operator-sdk edit --plugins=helm/v1-alpha
```

2. See that a chart was generated under 'dist/chart', and users
can obtain this solution from there.

**NOTE:** If you change the project, you need to update the Helm Chart
using the same command above to sync the latest changes. Furthermore,
if you create webhooks, you need to use the above command with
the '--force' flag and manually ensure that any custom configuration
previously added to 'dist/chart/values.yaml' or 'dist/chart/manager/manager.yaml'
is manually re-applied afterwards.

## Contributing
// TODO(user): Add detailed information on how you would like others to contribute to this project

**NOTE:** Run `make help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

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

