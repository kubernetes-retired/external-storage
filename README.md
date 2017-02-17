# External Provisioners
This repository houses community-maintained external provisioners plus a helper library for building them. Each provisioner is contained in its own directory so for information on how to use one, enter its directory and read its documentation. The library is contained in the `lib` directory.

## What is an 'external provisioner'?
External provisioners work just like in-tree dynamic PV provisioners. A `StorageClass` object can specify an external provisioner instance to be its `provisioner` like it can in-tree provisioners such as GCE or AWS. The instance will then watch for `PersistentVolumeClaims` that ask for the `StorageClass` and automatically create `PersistentVolumes` for them. For more information on how dynamic provisioning works, see [the docs](http://kubernetes.io/docs/user-guide/persistent-volumes/) or [this blog post](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html).

## How to use the library
```go
import (
  "github.com/kubernetes-incubator/nfs-provisioner/lib/controller"
)
```
You need to implement the `Provisioner` interface then pass your implementation to a `ProvisionController` and run the controller. The controller takes care of deciding when to call your implementation's `Provision` or `Delete`. The interface and controller are defined in the above package.

Note that because your provisioner needs to depend also on [client-go](https://github.com/kubernetes/client-go) and the library itself depends on a specific version of client-go, to avoid a dependency conflict you must ensure you use the exact same version of client-go as the library.

For a full guide on how to write an external provisioner using the library that demonstrates the above, see [here](docs/demo/hostpath-provisioner/).

If you want your provisioner to be compatible with users' RBAC/PSP/OpenShift authorization policies also consider reading [this](docs/authorization.md).
