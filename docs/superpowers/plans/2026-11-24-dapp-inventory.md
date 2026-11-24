# ORCA DappInventory and Gateway Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ORCA keeps a per-node `DappInventory`, runs one Gateway Deployment per node that hosts dApps, issues per-dApp E3 tokens, and reports the lifecycle through `GatewayReady`/`Installed`/`Registered`/`Advertised`/`Ready` conditions.

**Architecture:**
- The existing `DappManifestReconciler` gains three parts: a finalizer, inventory bookkeeping (pure helpers in `inventory.go`), and Gateway Deployment management (`gateway.go`).
- The Gateway itself (C++) is out of scope. Tests play its role by writing `DappInventory.status` and the Deployment status by hand (envtest runs no built-in controllers).

**Tech Stack:** Go 1.24, controller-runtime v0.21, kubebuilder markers, Flux helm-controller API v1.1.0, Ginkgo/Gomega + envtest (`make test`).

**Spec:** `docs/design/dapp-lifecycle.md`

## Global Constraints

- Never use em dashes anywhere (code comments, commit messages, docs).
- CRD group `cache.orca.com`, version `v1alpha1`.
- dApp ID range is `1..100`, unique per node (matches `E3-DAppID ::= INTEGER (1..100)` in dApp-libe3 `e3ap-1.0.0.asn1`).
- `DappInventory` is cluster-scoped and named after the node.
- `DappManifest.spec.nodeSelector` is removed. `nodeName` is required and immutable. `tolerations` stays.
- ORCA never creates, labels, or patches Nodes.
- Only the token hash (SHA-256, hex) is stored in `DappInventory.spec`. The plain token lives only in a Secret.
- Work on a new branch `feat/dapp-inventory` from `main`. The first commit also adds `docs/design/dapp-lifecycle.md` and this plan.

## Review Focus

1. **Crash between the inventory write and the Secret write.** Next reconcile must notice the Secret's token hash does not match the entry and rotate again, never leave the dApp with a token the Gateway rejects forever. Test in Task 4.
2. **Two DappManifests on the same node reconciled concurrently.** Both write the same `DappInventory`. A conflict must requeue (existing `IsConflict` pattern), and the two must never end up with the same dApp ID. Test in Task 2 (allocation) and Task 4 (two DMs, distinct IDs).
3. **101st dApp on one node.** Allocation fails, giving `Ready=False` with reason `InventoryUpdateFailed`. No HelmRelease is created. Test in Task 4.
4. **Changing `nodeName` on an existing DappManifest.** Rejected by the API server (CEL immutability), so the dApp can never sit in two inventories. Test in Task 1.
5. **Deleting a DappManifest whose Gateway never advertised it** (for example, the Gateway never got Ready). Deletion must not hang: no `Advertised=true` status entry means it is already withdrawn. Test in Task 5. Known limit, not handled: a Gateway that advertised and then died blocks deletion until its status is cleared. This is documented in the spec as intended behavior.

---

### Task 1: API types (DappInventory CRD, DappManifest nodeName and status)

**Files:**
- Create: `api/v1alpha1/dappinventory_types.go`
- Modify: `api/v1alpha1/dappmanifest_types.go` (spec and status structs)
- Modify: `config/samples/cache_v1alpha1_dappmanifest.yaml` (replace `nodeSelector` with `nodeName: o-du`)
- Modify: `internal/controller/dappmanifest_controller.go` (`buildSchedulingPostRenderer`: `nodeSelector` becomes `nodeName`)
- Modify: `internal/controller/dappmanifest_controller_test.go` (add `NodeName: "node-a"` to the existing fixture)
- Generated: `config/crd/bases/*`, `zz_generated.deepcopy.go`, `config/rbac/*` via `make manifests generate`

**Interfaces:**
- Produces (package `v1alpha1`):

```go
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
type DappInventory struct { metav1.TypeMeta; metav1.ObjectMeta; Spec DappInventorySpec; Status DappInventoryStatus }
type DappInventoryList struct { ... }

type DappRef struct {
    Namespace string `json:"namespace"`
    Name      string `json:"name"`
}
type DappInventoryEntry struct {
    Dapp       DappRef `json:"dapp"`
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=100
    DappID     int32   `json:"dappId"`
    TokenHash  string  `json:"tokenHash"`
    Generation int64   `json:"generation"`
}
type DappInventorySpec struct {
    // +listType=map
    // +listMapKey=dappId
    Entries []DappInventoryEntry `json:"entries,omitempty"`
}
type DappInventoryEntryStatus struct {
    DappID     int32 `json:"dappId"`
    Generation int64 `json:"generation"`
    Registered bool  `json:"registered"`
    Advertised bool  `json:"advertised"`
}
type DappInventoryStatus struct {
    // +listType=map
    // +listMapKey=dappId
    Entries []DappInventoryEntryStatus `json:"entries,omitempty"`
}
```

- `DappManifestSpec`: remove `NodeSelector`. Add:

```go
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="nodeName is immutable"
NodeName string `json:"nodeName"`
```

- `DappManifestStatus`: add `DappID int32 json:"dappId,omitempty"` and `NodeName string json:"nodeName,omitempty"`.

Steps:

- [ ] **Step 1: Create the branch and commit the design doc**

```bash
git switch main && git switch -c feat/dapp-inventory
git add docs/design/dapp-lifecycle.md docs/superpowers/plans/2026-11-24-dapp-inventory.md
git commit -m "docs: dApp lifecycle design and implementation plan"
```

- [ ] **Step 2: Write the failing test** in `dappmanifest_controller_test.go`: `It("rejects changing nodeName")`. Create a DM with `NodeName: "node-a"`, update it to `"node-b"`, and `Expect(err).To(MatchError(ContainSubstring("nodeName is immutable")))`. Also assert that the existing HelmRelease test now sees a post-renderer patch containing `nodeName: node-a`.
- [ ] **Step 3: Run** `make test`. Expected: compile failure (no `NodeName` field).
- [ ] **Step 4: Implement the types above.** In `buildSchedulingPostRenderer`, set `podSpec["nodeName"] = Spec.NodeName` (always present now, so drop the early `nil` return). Run `make manifests generate`, and register `DappInventory`/`DappInventoryList` in `init()` with `SchemeBuilder.Register`.
- [ ] **Step 5: Run** `make test`. Expected: PASS.
- [ ] **Step 6: Commit** with `feat(api): add DappInventory CRD and DappManifest nodeName`.

---

### Task 2: Inventory helpers (ID allocation, token)

**Files:**
- Create: `internal/controller/inventory.go`
- Test: `internal/controller/inventory_test.go` (plain `testing`, no envtest)

**Interfaces:**
- Consumes: Task 1 types.
- Produces:

```go
const minDappID, maxDappID int32 = 1, 100
func allocateDappID(entries []cachev1alpha1.DappInventoryEntry, ref cachev1alpha1.DappRef) (int32, error)
func newToken() (token, hash string, err error)
func hashToken(token string) string
func findEntry(entries []cachev1alpha1.DappInventoryEntry, ref cachev1alpha1.DappRef) int // index or -1
func findEntryStatus(status []cachev1alpha1.DappInventoryEntryStatus, id int32) *cachev1alpha1.DappInventoryEntryStatus
```

Steps:

- [ ] **Step 1: Write the failing tests:**
  - `TestAllocateDappIDReturnsExisting`: the ref is already in entries with ID 7, so the result is 7.
  - `TestAllocateDappIDLowestFree`: entries use IDs {1,2,4}, so a new ref gets 3.
  - `TestAllocateDappIDFull`: 100 entries, so a new ref gets a non-nil error.
  - `TestNewTokenHash`: `hashToken(token) == hash`, `len(hash) == 64`, and two calls give different tokens.
- [ ] **Step 2: Run** `go test ./internal/controller -run 'TestAllocate|TestNewToken'`. Expected: FAIL (undefined).
- [ ] **Step 3: Implement.** `newToken` takes 32 bytes from `crypto/rand`, hex encoded. `hashToken` is `sha256.Sum256`, hex encoded.
- [ ] **Step 4: Run** the same command. Expected: PASS.
- [ ] **Step 5: Commit** with `feat(controller): inventory ID allocation and token helpers`.

---

### Task 3: Gateway Deployment and its RBAC

**Files:**
- Create: `internal/controller/gateway.go`
- Modify: `internal/controller/dappmanifest_controller.go` (reconciler fields, RBAC markers)
- Modify: `cmd/main.go` (flags `--gateway-image`, `--gateway-namespace` default `orca-system`)
- Modify: `config/manager/manager.yaml` (add `--gateway-image=ghcr.io/pedromartinssouza/orca-gateway:latest` to args)
- Create: `config/rbac/gateway_service_account.yaml` (name `gateway`, which kustomize prefixes to `orca-gateway`), `config/rbac/gateway_role.yaml` (ClusterRole: `dappinventories` get/list/watch, `dappinventories/status` get/patch), `config/rbac/gateway_role_binding.yaml`
- Modify: `config/rbac/kustomization.yaml`
- Test: `internal/controller/gateway_test.go` (Ginkgo, envtest)

**Interfaces:**
- Consumes: Task 1 `DappInventory`.
- Produces:

```go
// fields added to DappManifestReconciler
GatewayImage     string
GatewayNamespace string

const gatewayServiceAccount = "orca-gateway"
func gatewayName(node string) string // "orca-gateway-" + node
func (r *DappManifestReconciler) ensureGateway(ctx context.Context, inv *cachev1alpha1.DappInventory) (ready bool, err error)
```

The Deployment is built as follows:
- name `gatewayName(inv.Name)` in `r.GatewayNamespace`, `replicas: 1`
- pod `nodeName: inv.Name`, `serviceAccountName: gatewayServiceAccount`
- one container named `gateway`, image `r.GatewayImage`, with env `NODE_NAME` from `fieldRef: spec.nodeName`
- labels `app.kubernetes.io/name: orca-gateway`
- controller ownerReference set to `inv`

It is ready when `Status.AvailableReplicas >= 1`. RBAC markers to add: `apps/deployments` get;list;watch;create;update;patch;delete, `dappinventories` get;list;watch;create;update;patch;delete, `dappinventories/status` get, `secrets` get;list;watch;create;update;patch;delete, `namespaces` get;create.

Steps:

- [ ] **Step 1: Write the failing test** `Describe("ensureGateway")`:
  - Create a `DappInventory` named `node-a` and call `ensureGateway`. Expect `ready == false` and a Deployment `orca-gateway-node-a` in `orca-system` with `Spec.Template.Spec.NodeName == "node-a"`, `ServiceAccountName == "orca-gateway"`, image equal to the configured one, and `OwnerReferences[0].Name == "node-a"`.
  - Then set `Status.AvailableReplicas = 1` through `k8sClient.Status().Update` and call again. Expect `ready == true`.
  - The test creates the `orca-system` namespace in `BeforeEach` if it is missing.
- [ ] **Step 2: Run** `make test`. Expected: FAIL (undefined `ensureGateway`).
- [ ] **Step 3: Implement** with `controllerutil.CreateOrUpdate`, following `reconcileHelmRelease`. Add the flags, wire them into `controller.DappManifestReconciler{...}` in `cmd/main.go`, add the RBAC YAML, and run `make manifests`.
- [ ] **Step 4: Run** `make test`. Expected: PASS.
- [ ] **Step 5: Commit** with `feat(controller): per-node Gateway Deployment owned by DappInventory`.

---

### Task 4: Create/update flow and conditions

**Files:**
- Modify: `internal/controller/dappmanifest_controller.go` (`Reconcile`, new `reconcileInventory`, `reconcileTokenSecret`, `syncLifecycleConditions`, HelmRelease values)
- Test: `internal/controller/dappmanifest_controller_test.go`

**Interfaces:**
- Consumes: Task 2 helpers, Task 3 `ensureGateway`.
- Produces:

```go
const dappFinalizer = "cache.orca.com/dapp-inventory"
func tokenSecretName(dm *cachev1alpha1.DappManifest) string // dm.Name + "-e3-token", in dm.Spec.Namespace, key "token"
func (r *DappManifestReconciler) reconcileInventory(ctx context.Context, dm *cachev1alpha1.DappManifest) (*cachev1alpha1.DappInventory, cachev1alpha1.DappInventoryEntry, error)
```

- The chart contract is that the HelmRelease gets the values `{"orca": {"e3TokenSecret": tokenSecretName(dm)}}`, and the dApp chart mounts that Secret at `/var/run/orca/e3/token` (spec, "How the token reaches the dApp"). Reading that path in the E3 client library is out of scope for this plan.
- `dm.Spec.Namespace` must be set, or reconcile fails with `Ready=False` and reason `InvalidSpec`.

**Reconcile order (not deleting):**
1. Add the finalizer.
2. Get or create `DappInventory` `dm.Spec.NodeName`.
3. Call `ensureGateway`. If it is not ready, set `GatewayReady=False`, reason `Pending`, write the status, and return without requeue (the Deployment watch from Task 6 triggers the next reconcile). Otherwise set `GatewayReady=True`.
4. `reconcileInventory`:
   - Allocate the ID.
   - Rotate the token if the entry is missing, if `entry.Generation != dm.Generation`, or if the Secret is missing, or if `hashToken(secret.Data["token"]) != entry.TokenHash`.
   - On rotation, write the inventory spec (new hash, `Generation = dm.Generation`), then ensure the target namespace exists, then write the Secret.
   - Any error gives `Ready=False`, reason `InventoryUpdateFailed`.
5. HelmRepository and HelmRelease, as today, plus the values above.
6. Conditions:
   - `Installed` as today.
   - `Registered` and `Advertised` from `findEntryStatus(inv.Status.Entries, id)`. They count only when the status `Generation == entry.Generation`. Otherwise both are False with reason `Pending`.
   - `Ready` is True with reason `Reconciled` only when all four are True. Otherwise it is False with reason `Pending`.
   - Set `Status.DappID` and `Status.NodeName`.
7. Remove the `RequeueAfter: 10s`.

Steps:

- [ ] **Step 1: Write the failing tests.** Replace the existing `It` with these. Each test reconciles, then sets the Gateway Deployment `AvailableReplicas = 1` and reconciles again.
  - `It("assigns a dApp ID, stores only the token hash, and creates the Secret")`: inventory `node-a` has an entry with `DappID == 1` and `TokenHash == hashToken(string(secret.Data["token"]))`. The HelmRelease values contain `orca.e3TokenSecret`. `Status.DappID == 1`, `Status.NodeName == "node-a"`.
  - `It("reports Ready only after the Gateway registers and advertises")`: before any status write, `Ready=False`, `GatewayReady=True`, and `Registered=False`. Then write `inv.Status.Entries = [{DappID:1, Generation:<entry gen>, Registered:true, Advertised:true}]`, set HelmRelease status `Ready=True`, and reconcile. Expect `Registered`, `Advertised`, `Installed` and `Ready` all True.
  - `It("rotates the token when the spec changes")`: change `Spec.Helm.Version`, then reconcile. The entry generation equals the new `dm.Generation`, the hash differs, and `Registered=False` until the status catches up.
  - `It("rotates the token when the Secret is missing")`: delete the Secret, then reconcile. The new Secret's hash equals the entry hash (Review Focus 1).
  - `It("gives distinct IDs to two dApps on the same node")`: the IDs are 1 and 2, and there is one Deployment for the node (Review Focus 2).
  - `It("fails with InventoryUpdateFailed when the node is full")`: pre-fill the inventory with 100 entries, then reconcile. `Ready=False` with reason `InventoryUpdateFailed`, and the HelmRelease is not found (Review Focus 3).
- [ ] **Step 2: Run** `make test`. Expected: FAIL.
- [ ] **Step 3: Implement** the reconcile order above.
- [ ] **Step 4: Run** `make test`. Expected: PASS.
- [ ] **Step 5: Commit** with `feat(controller): inventory, token and lifecycle conditions on create/update`.

---

### Task 5: Deletion through the finalizer

**Files:**
- Modify: `internal/controller/dappmanifest_controller.go` (`reconcileDelete`)
- Test: `internal/controller/dappmanifest_controller_test.go`

**Interfaces:**
- Consumes: Task 4 `dappFinalizer`, `findEntry`, `findEntryStatus`.
- Produces: `func (r *DappManifestReconciler) reconcileDelete(ctx context.Context, dm *cachev1alpha1.DappManifest) (ctrl.Result, error)`

**Order** (when `DeletionTimestamp` is set):
0. If the finalizer is absent, return. If the inventory does not exist, skip to step 5.
1. Set `Ready=False`, reason `Deleting`.
2. Remove the entry from the inventory spec.
3. If `findEntryStatus` reports `Advertised=true` for its ID, write the status and return. The inventory watch triggers the next reconcile.
4. Otherwise set `Advertised=False`, reason `Withdrawn`.
5. Delete the token Secret.
6. If the inventory spec has no entries left, delete the inventory (garbage collection removes the Gateway).
7. Remove the finalizer. The HelmRepository and HelmRelease are removed by garbage collection through the existing owner references, after the un-advertise.

The existing `AfterEach` must reconcile once after `Delete` so the finalizer clears.

Steps:

- [ ] **Step 1: Write the failing tests:**
  - `It("waits for the Gateway to withdraw before removing the finalizer")`: the dApp is advertised (status entry `Advertised=true`). Delete and reconcile. The entry is gone from the spec, the DM still exists with `Ready` reason `Deleting`. Set the status entry to `Advertised=false` and reconcile. The DM is gone, the Secret is gone, and the inventory is gone.
  - `It("deletes a never-advertised dApp without waiting")`: no status entries. A single reconcile after delete removes the DM (Review Focus 5).
  - `It("keeps the inventory while another dApp remains on the node")`: two DMs, delete one. The inventory still exists and has one entry.
- [ ] **Step 2: Run** `make test`. Expected: FAIL.
- [ ] **Step 3: Implement** `reconcileDelete` and call it at the top of `Reconcile`.
- [ ] **Step 4: Run** `make test`. Expected: PASS.
- [ ] **Step 5: Commit** with `feat(controller): finalizer waits for Gateway un-advertise`.

---

### Task 6: Watches and concurrency

**Files:**
- Modify: `internal/controller/dappmanifest_controller.go` (`SetupWithManager`)
- Test: `internal/controller/dappmanifest_controller_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces: the field index `const nodeNameIndex = "spec.nodeName"` on `DappManifest`, with the extractor `func nodeNameIndexer(obj client.Object) []string`, and `func (r *DappManifestReconciler) dappsOnNode(ctx context.Context, node string) []reconcile.Request` (a `List` with `client.MatchingFields{nodeNameIndex: node}`).

`SetupWithManager`:
- Index `spec.nodeName` with `mgr.GetFieldIndexer().IndexField`.
- `For(DappManifest)` and keep the two `Owns`.
- `Watches(&DappInventory{}, handler.EnqueueRequestsFromMapFunc(...))`, mapping `inv.Name` to `dappsOnNode`.
- `Watches(&appsv1.Deployment{}, ...)`, mapping the controller ownerReference of kind `DappInventory` to `dappsOnNode(owner.Name)`.
- `WithOptions(controller.Options{MaxConcurrentReconciles: 4})`.

Steps:

- [ ] **Step 1: Write the failing test** `TestNodeNameIndexer` (plain `testing`): a DM with `NodeName: "node-a"` gives `[]string{"node-a"}`. The suite's client has no cache, so `dappsOnNode` itself is checked in Step 4 against a real cluster, not in envtest.
- [ ] **Step 2: Run** `go test ./internal/controller -run TestNodeNameIndexer`. Expected: FAIL.
- [ ] **Step 3: Implement** the code above, then run `make test`. Expected: PASS.
- [ ] **Step 4: Verify the watches live.** On the sandbox cluster: `make install`, then `make run ARGS=--gateway-image=busybox:1.36`. Apply a DM with `nodeName` set to the `o-du` node. Patch its DappInventory status with `kubectl patch dappinventory <node> --subresource=status --type=merge -p '{"status":{"entries":[{"dappId":1,"generation":1,"registered":true,"advertised":true}]}}'`. Expected: within a second, a `reconciling dappmanifest` log line for that DM and `Registered=True` in `kubectl get dappmanifest -o yaml`, with no 10s polling between.
- [ ] **Step 5: Commit** with `feat(controller): watch DappInventory and Gateway Deployments, drop polling`.
