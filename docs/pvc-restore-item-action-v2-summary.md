# PVC RestoreItemActionV2: Summary for Velero 1.18

## Why the PVC RestoreItemActionV2 Was Created

### Velero 1.18 Phase Changes

Velero 1.18 introduced new **intermediate restore phases**:

- `WaitingForPluginOperations` / `WaitingForPluginOperationsPartiallyFailed` — restore is waiting for async plugin operations to complete
- `Finalizing` / `FinalizingPartiallyFailed` — restore of resources and async ops are done; Velero is running wrap-up tasks (e.g. PV patching, exec hooks) before moving to a terminal phase

The restore flow is now:

`InProgress` → (if async ops) `WaitingForPluginOperations` → `Finalizing` → `Completed` (or `PartiallyFailed` / `Failed`).

### Root Cause of Restores Stuck in Finalizing

When restoring PVCs backed by **CSI snapshots**:

1. Velero creates the PVC with a `dataSource` pointing to a `VolumeSnapshot`.
2. The CSI provisioner dynamically provisions a PV and binds it to the PVC — **this can take time**.
3. If **no RestoreItemActionV2** registers an async operation for those PVCs, Velero has nothing to wait on and moves directly from `InProgress` to `Finalizing`.
4. At that moment, **RestoreVolumeInfoTracker.Populate()** runs. It inspects restored PVCs; if they are not yet bound, it logs "PVC is not bound or has no volume name" and **skips** adding them to the volume info used for PV patching.
5. The **restore finalizer controller** then runs with an incomplete (or empty) work list: it has no PVs to patch and no async operations to track, so it never transitions the restore out of `Finalizing`, and the restore appears stuck.

So the hang is not because KubeVirt controllers block patching; it is because the finalizer has no work to do (volume info was captured when PVCs were still unbound) and no path to mark the restore complete.

### Fix: Register an Async Operation for PVC Binding

By implementing a **RestoreItemActionV2** for PVCs that:

1. **Execute()** — Performs the same label cleanup as the old V1 action and returns an **OperationID** (e.g. `pvc-binding:<namespace>/<name>`) for each restored PVC.
2. **Progress()** — Checks via the Kubernetes API whether that PVC is in `Bound` phase; returns `Completed: true` when bound, `Completed: false` otherwise.

Velero then:

- Keeps the restore in **WaitingForPluginOperations** until all such operations complete.
- Only after that moves to **Finalizing**, at which point PVCs are already bound and **RestoreVolumeInfoTracker.Populate()** sees them, so the finalizer has a proper work list and can finish (patch PVs, run hooks, then set phase to `Completed`).

So the PVC RestoreItemActionV2 was created to **align with Velero 1.18’s async operation model** and prevent restores from entering Finalizing before PVCs are bound, eliminating the “stuck in Finalizing” behavior.

---

## Changes Made

### 1. New plugin: `pkg/plugin/pvc_restore_item_action_v2.go`

- Implements the Velero **RestoreItemAction V2** interface (same package as other restore actions, different interface).
- **Name()** — `"kubevirt-velero-plugin/restore-pvc-action-v2"`.
- **AppliesTo()** — `PersistentVolumeClaim` (unchanged from V1).
- **Execute()** — Same label/annotation cleanup as the previous PVC restore action; for PVCs that are not skipped (`AnnInProgress` not set), returns `WithOperationID("pvc-binding:<namespace>/<name>")`.
- **Progress(operationID, restore)** — Resolves namespace/name from the operation ID (including restore namespace mapping), uses a Kubernetes client to get the PVC; returns `OperationProgress{Completed: true}` when `Phase == ClaimBound`, otherwise in-progress.
- **Cancel()** — No-op.
- **AreAdditionalItemsReady()** — Returns `true, nil`.

The plugin holds a `logrus.FieldLogger` and a `kubernetes.Interface` (from `util.GetK8sClient()` in the constructor).

### 2. Registration in `main.go`

- Replaced **RegisterRestoreItemAction** for the PVC restore action with **RegisterRestoreItemActionV2**.
- Constructor **newPVCRestoreItemActionV2** builds the k8s client and returns `NewPVCRestoreItemActionV2(logger, client)`.

### 3. Removal of V1 PVC restore action

- **Deleted** `pkg/plugin/pvc_restore_item_action.go` (V1 implementation) so only the V2 plugin handles PVC restores.

### 4. Reverted “Finalizing as terminal” workarounds

Treating `Finalizing` as a terminal state was a workaround that hid the real bug. All such handling was reverted so that restores must reach `Completed` (or `PartiallyFailed` / `Failed`) for success/failure checks.

- **cmd/velero-backup-restore/velero-backup-restore.sh**
  - `verify_restore_completion()` — Only accepts `Completed` as success; removed `Finalizing` and `FinalizingPartiallyFailed`.
  - `verify_selective_restore_completion()` — Only accepts `Completed` or `PartiallyFailed`; removed `Finalizing`; added explicit handling for `Failed`.
- **tests/framework/externalBackup.go** — Restore completion checks no longer accept `RestorePhaseFinalizing`.
- **tests/pvc_vs_labeling_test.go** — Same; terminal states are only `Completed` or `PartiallyFailed`, with failure handling for `Failed` / `FailedValidation`.

### 5. Tests: `pkg/plugin/pvc_restore_item_action_test.go`

- Switched to testing the **V2** plugin using a fake Kubernetes clientset.
- **TestPvcRestoreV2Execute** — Covers the same Execute scenarios as before (skip in-progress, label cleanup, operation ID set when not skipped).
- **TestPvcRestoreV2Progress** — Covers: PVC not found (in progress), PVC Pending (in progress), PVC Bound (completed), invalid operation ID, malformed key, and namespace remapping.

---

## Result

- Restores that include PVCs backed by CSI snapshots now stay in **WaitingForPluginOperations** until those PVCs are bound, then move to **Finalizing** with correct volume info, and the finalizer can complete the restore to **Completed**.
- The plugin no longer relies on treating **Finalizing** as terminal; behavior is aligned with Velero 1.18’s phase model and async operation flow.
