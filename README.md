# nfs-provisioner
nfs-provisioner is an out-of-tree dynamic provisioner for Kubernetes. It automatically creates NFS `PersistentVolumes` for `PersistentVolumeClaims` that request a `StorageClass` configured to use some instance of nfs-provisioner as their provisioner. For more information see http://kubernetes.io/docs/user-guide/persistent-volumes/ and https://github.com/kubernetes/kubernetes/pull/30285.

Two goals:
* Demonstrate how to implement an out-of-tree/external provisioner
* Deploy shared storage anywhere, easily

## Running
To deploy nfs-provisioner on a Kubernetes cluster see [Deployment](docs/deployment.md).

To use nfs-provisioner once it is deployed see [Usage](docs/usage.md).

For information on running multiple instances of nfs-provisioner see [Running Multiple Provisioners](docs/multiple.md).

## TODO
>Currently, the provisioner creates the NFS shares that back provisioned `PersistentVolumes` by making unique, deterministically named directories in `/export` for each volume. No quotaing or security/permissions yet.

