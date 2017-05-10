# v1.0.8
- Add mountOptions StorageClass parameter (#84) (see [Usage](./docs/usage.md) for complete SC parameter info)
- Replace root-squash argument with a rootSquash SC parameter (#86) (see [Usage](./docs/usage.md) for complete SC parameter info)
	- If the root-squash argument is specified, the provisioner will fail to start; please if you're using it, convert to the SC parameter before updating!
- Watch for unexpected stop of ganesha.nfsd and restart if seen (#98). This is a simple health check that mitigates NFS ganesha crashes which are under investigation (but probably out of the provisioner's control to prevent at the moment).

# v1.0.7
- Set a high limit for maximum number of files Ganesha may have open (setrlimit RLIMIT_NOFILE) -- this requires the additional SYS_RESOURCE capability, if not available the provisioner will still start but with a warning

# v1.0.6
- Reduce image size by a lot

# v1.0.5
- Add compatibility with kubernetes v1.6.x (using lib v2.0.x)

# v1.0.4
- Add `server-hostname` flag

# Rename kubernetes-incubator/nfs-provisioner to kubernetes-incubator/external-storage
- The previous releases were done when the repo was named nfs-provisioner: http://github.com/kubernetes-incubator/nfs-provisioner/releases. Newer releases done here in external-storage will *not* have corresponding git tags (external-storage's git tags are reserved for versioning the library), so to keep track of releases check this changelog, the [README](README.md), or [Quay](https://quay.io/repository/kubernetes_incubator/nfs-provisioner)

# v1.0.3
- Fix inability to export NFS shares ("1 validation errors in block FSAL") when using Docker's overlay storage driver (CoreOS/container linux, GCE) by patching Ganesha to use device number as fsid. (#63)
- Adds configurable number of retries on failed Provisioner operations. Configurable as an argument to `NewProvisionController`. nfs-provisioner defaults to 10 retries unless the new flag/argument is used. (#65)

# v1.0.2
- Usage demo & how-to for writing your own external PV provisioner added here https://github.com/kubernetes-incubator/nfs-provisioner/tree/master/demo
- Change behaviour for getting NFS server IP from env vars (node, service) in case POD_IP env var is not set when needed. Use `hostname -i` as a fallback only for when running out-of-cluster (#52)
- Pass whole PVC object from controller to `Provision` as part of `VolumeOptions`, like upstream (#48)
- Filter out controller's self-generated race-to-lock leader election PVC updates from being seen as forced resync PVC updates (#58) 
- Fix controller's event watching for ending race-to-lock leader elections early. Now correctly discover the latest `ProvisionFailed`/`ProvisionSucceeded` events on a claim (#59)

# v1.0.1
- Add rootsquash flag for enabling/disabling rootsquash https://github.com/kubernetes-incubator/nfs-provisioner/pull/40

# v1.0.0
- Automatically create NFS PVs for any user-defined Storage Class of PVCs, backed by a containerized NFS server that creates & exports shares from some user-defined mounted storage
- Support multiple ways to run:
  - standalone Pod, e.g. for easy dynamically provisioned scratch space
  - stateful app, either as a StatefulSet or Deployment of 1 replica: the NFS server will survive restarts and its provisioned PVs can be backed by some mounted persistent storage e.g. a hostPath or one big PV
  - DaemonSet, where each node runs the NFS server to expose its hostPath storage
  - Docker container or binary outside of Kube
- Race-to-lock PVCs: when multiple instances are running & serving the same PVCs, only one attempts to provision for a PVC at a time
- Optionally exponentially backoff from calls to Provision() and Delete()
- Optionally set per-PV filesystem quotas: based on XFS project-level quotas and available only when running outside of Kubernetes (pending mount option support in Kube)

Docker image:
`quay.io/kubernetes_incubator/nfs-provisioner:v1.0.0`
