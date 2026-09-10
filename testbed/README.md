# Testbed provisioning

Scripts to build the full dissertation testbed from nothing, and tear it
down again, matching the Ch.5 §5.1 claim that the Kind-provisioned cluster
"can be fully torn down and recreated between runs, ensuring a clean
baseline for each experiment." Each numbered script is idempotent; run them
individually or via `deploy-all.sh`.

## What gets deployed

| Step | Script | What |
|---|---|---|
| 1 | `01-create-cluster.sh` | 4-node Kind cluster (`kind-setup/kind-config.yaml`): control-plane, `components` worker, tainted `o-du` worker, tainted `o-cu` worker |
| 2 | `02-install-near-rt-ric.sh` | Near-RT RIC platform (`ricplt`/`ricinfra`) via the official `ric-dep` installer, pinned commit + local patches |
| 3 | `03-install-fluxcd.sh` | FluxCD controllers (no Git source — ORCA drives `HelmRelease`/`HelmRepository` objects directly) |
| 4 | `04-install-orca.sh` | ORCA operator from its published OCI chart |
| 5 | `05-install-non-rt-ric.sh` | Minimal Non-RT RIC: Policy Management Service only (see below) |
| 6 | `06-deploy-sample-dapp.sh` | Sample `DappManifest` CR, exercising the full LCM path |

Run everything: `./deploy-all.sh`. Tear down: `./teardown.sh` (add `--clean`
to also wipe the cached `it/dep`/chart downloads in `.build/`).

## Why the Non-RT RIC slice is PMS-only

The official Non-RT RIC install path moved from a standalone
`deploy-nonrtric` script (what older docs still describe) to a full
ONAP-based SMO installer (`it/dep/smo-install`) requiring 64GB RAM / 20
vCPU / 100GB disk — infeasible here and out of scope for this project's
"minimal components, not production-ready" premise (Ch.4 §4.2).

ORCA does **not** need to be deployed as an rApp to satisfy "ORCA shall be
deployed at the SMO level, thus within the Non-RT RIC scope" (Ch.4 §4.2):
rApps are pluggable workloads that consume the Non-RT RIC platform's own
services over R1 (PMS, DME/ICS, SME); ORCA consumes none of them; it talks
to Kubernetes and FluxCD directly. It's closer to a platform-level SMO
component than a pluggable rApp, the same way Near-RT RIC's own `e2mgr`/
`submgr`/`rtmgr` in `ricplt` aren't xApps. So "within Non-RT RIC scope"
is read as SMO-layer placement, not literal rApp packaging — ORCA keeps
running as its own Helm release in `orca-system`, not migrated into the
`nonrtric` namespace or onboarded through rApp Manager.

rApp Manager itself was evaluated and deliberately excluded: its only
deployment backend is ONAP ACM (confirmed from the
[nonrtric-plt-rappmanager](https://github.com/o-ran-sc/nonrtric-plt-rappmanager)
source — no config swaps in a lighter backend), which pulls the full ONAP
stack back in. The project also marks itself `not-for-production`,
`experimental`, and "purely a prototype" packaging model. Running real
rApps is therefore a separate, larger undertaking (full ONAP stack on
properly-sized hardware) than this testbed provisions — revisit if/when
Ch.5's evaluation scenarios actually require one.

What's deployed instead is just the two charts PMS actually needs:

- `policymanagementservice` — the A1 policy manager; the only Non-RT RIC
  component doing real work here, since it's what talks A1 to the Near-RT
  RIC's `a1mediator`
- `nonrtric-common` — a shared template library PMS's chart depends on,
  vendored locally (its `Chart.yaml` points at a `@local` chartmuseum
  alias we don't run)

`values/pms-values-override.yaml` repoints PMS's default config (which
targets simulated RICs behind an ONAP SDNC controller, plus ONAP DMaaP for
event streaming) at the real `a1mediator` directly, with the SDNC
controller indirection and DMaaP streaming both dropped.

**Chart bug found and fixed along the way:** with only the RIC/controller
override applied, PMS came up healthy but reported zero RICs
(`GET /a1-policy/v2/rics` → `{"rics":[]}`) with no error anywhere in its
logs. Root-caused by reading the deployed image's actual source
(`nonrtric-plt-a1policymanagementservice` tag `2.11.0`, via
`RefreshConfigTask`/`ConfigurationFile`/`ApplicationConfig`): the chart's
init container seeds `application_configuration.json` into the PVC at
`/var/policy-management-service/`, but the app's own `app.filepath`
default points at `/opt/app/policy-agent/data/application_configuration.json`
-- a path nothing ever populates. Since `ConfigurationFile.getLastModified()`
returns `0` for a missing file, which equals the class's own
zero-initialized "last seen" value, the refresh loop's every-60s check
silently short-circuits forever without ever attempting a read (hence no
log line, healthy or not). `values/pms-values-override.yaml` overrides
`application.app.filepath` to the path the init container actually writes
to, which fixes it. Verified end to end with `DEBUG` logging temporarily
enabled: PMS made a real `GET .../A1-P/v2/policytypes` call against the
live `a1mediator`, got `200 OK`, detected protocol `STD_V2_0_0`, and set
the RIC to `AVAILABLE` -- a genuine, working A1 connection between this
Non-RT RIC and the existing Near-RT RIC, not just two healthy pods sitting
next to each other.

**Known issue to remember:** a `helm upgrade` that only changes a
ConfigMap does not restart the StatefulSet pod on its own (no checksum
annotation in this chart), so the running process keeps its stale config
until something forces a restart. `05-install-non-rt-ric.sh` handles this
by deleting the pod after every upgrade; if you hand-edit
`pms-values-override.yaml` and run `helm upgrade` yourself, remember to
`kubectl delete pod policymanagementservice-0 -n nonrtric` afterward.

Skipped entirely: A1 Simulator (a real Near-RT RIC is running), Control
Panel (UI convenience, not required), SME/DME/ICS (only needed for real
rApps).

## Provenance of the pinned versions / patches

Reverse-engineered from the environment as it was actually built (bash
history + on-disk clones), not guessed:

- `it/dep` commit `8f797d67` — has the `RECIPE_EXAMPLE/PLATFORM/` layout
  the install command expects (the standalone `ric-plt/ric-dep` repo's
  `RECIPE_EXAMPLE/` is flat, no `PLATFORM/` subdirectory)
- `ric-dep` submodule pinned separately to `348562bc` — the exact commit
  the live, validated environment was built from
- `patches/ric-dep-helm3-only.patch` — two changes found already applied
  (uncommitted) in the working `ric-dep` clone: drops the Helm2/3
  branching in `bin/install` (this environment is Helm3-only), and adds
  `--verify=false` to the `helm-servecm` plugin install in
  `bin/install_common_templates_to_helm.sh`

## Verified with a real teardown/rebuild

The full `01`-`06` sequence was run end to end against the live cluster
(`kind delete cluster` + full rebuild), not just reasoned through. Findings:

- `sudo` was **not** needed for the `ric-dep` install scripts (the original
  manual setup used it; turned out to be incidental, not load-bearing). All
  13 `ricplt`/`ricinfra` pods reached Ready within the 5-minute wait window
  on a completely clean cluster, no image-pull-secret or permission issues.
- The rebuild exposed a real, previously-latent ORCA bug: `dapp-sample`'s
  `HelmRelease` failed immediately with `namespaces "dapp-sample-system"
  not found` and then permanently stalled (helm-controller does not retry
  a stalled release on its own). This never surfaced before because
  `dapp-sample-system` already existed from much earlier manual work, so
  the true first-install path had never actually been exercised. Root
  cause and fix: [PR #5](https://github.com/pedromartinssouza/orca/pull/5)
  (`DappManifest` reconciliation never set `install.createNamespace` on
  the `HelmRelease` it generates). `06-deploy-sample-dapp.sh` pre-creates
  the target namespace itself as a workaround so this script keeps
  working against `ORCA_CHART_VERSION=0.5.0` (which predates the fix) --
  safe to drop once that version is bumped past the fix.
- PMS's `near-rt-ric-1` RIC came up `AVAILABLE` on the very first install
  attempt (no restart needed) -- confirming the `pms-values-override.yaml`
  fix is correctly baked into a fresh install, and the ConfigMap-restart
  quirk above only bites on `helm upgrade` of an existing release.
