# Running kubevirt-velero-plugin Tests Locally

This guide describes how to build and run the functional tests from your machine against an existing OpenShift cluster (e.g. with OADP and OpenShift Virtualization).

For partner compatibility testing and test labels, see [partner-compatibility-testing.md](partner-compatibility-testing.md).

## Prerequisites

- **Cluster**: OpenShift with [OpenShift Virtualization](https://docs.openshift.com/container-platform/latest/virt/about-virt.html) installed.
- **Backup/Restore**: [OADP](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/oadp/about.html) (or Velero) installed and configured with a valid backup storage location.
- **Local**: [Go](https://go.dev/dl/) installed (1.23+), and `kubectl` or `oc` in your `PATH`. `KUBECONFIG` must point to your cluster.

## Step 1: Build the test binary

From the repository root:

```bash
make tests-local
```

This compiles the Ginkgo test suite into `_output/tests/tests.test`. The Velero CLI is **not** required at build time; it is downloaded automatically when you run the tests (Step 3).

## Step 2: Set environment variables

```bash
export KUBECONFIG=~/.kube/config
export KUBEVIRT_PROVIDER=external
export KVP_STORAGE_CLASS=<your-storage-class>
export KVP_BACKUP_NS=openshift-adp
```

| Variable | Description |
|----------|-------------|
| `KUBECONFIG` | Path to kubeconfig for the cluster (e.g. `~/.kube/config`). |
| `KUBEVIRT_PROVIDER` | Set to `external` when using an existing cluster (not kubevirtci). |
| `KVP_STORAGE_CLASS` | StorageClass used for DataVolumes/PVCs in tests (e.g. `ocs-storagecluster-ceph-rbd-virtualization` on ODF). |
| `KVP_BACKUP_NS` | Namespace where Velero/OADP runs (e.g. `openshift-adp` for OADP). |

Optional:

- **Custom backup script**: `export BACKUP_SCRIPT_BIN=/path/to/your/script.sh`  
  If unset, the default [velero-backup-restore.sh](cmd/velero-backup-restore/velero-backup-restore.sh) is used (works with OADP/Velero).

## Step 3: Run the tests

### Partner compatibility tests only (recommended)

```bash
make test-functional-local TEST_ARGS="--test-args=-ginkgo.label-filter=PartnerComp"
```

### All functional tests

Omit the label filter:

```bash
make test-functional-local
```

### Other options

- **Focus a specific test**:  
  `make test-functional-local TEST_ARGS="--test-args=-ginkgo.focus=<regex>"`
- **Verbose**: `-ginkgo.v` is already passed by the test runner.

The test harness will download the Velero CLI (v1.16.0) to `_output/velero/bin/` on first run and add it to `PATH` for the test process. Default test timeout is 360 minutes.

## Troubleshooting

- **Go version**: The project targets Go 1.23. If the local build fails due to Go version mismatch, use the containerized build and run:  
  `make test-functional TEST_ARGS="--test-args=-ginkgo.label-filter=PartnerComp"`  
  (requires Podman or Docker and the builder image.)

- **Kubectl**: Tests use `kubectl` from the environment. With `KUBEVIRT_PROVIDER=external`, `KUBECONFIG` is used to find the cluster; ensure `kubectl` or `oc` works before running tests.

- **Storage class**: Use a StorageClass that supports ReadWriteOnce and is suitable for VMs (e.g. ODF’s `ocs-storagecluster-ceph-rbd-virtualization`).
