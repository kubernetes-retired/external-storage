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
* CI & testing
* Fix dependency vendoring (pending client-go things)
* Use-case documentation
* Security: respecting SCC & PSP, restrict each share to a GID & use supplemental groups feature for access (pending nfs-ganesha things)
* Privileged flag: is it needed now that NFS ganesha is being used, if not are any specific capabilities needed, etc.?
* Quotaing (currently size request is ignored)
* "Static" exports: expose existing exports to kubernetes
