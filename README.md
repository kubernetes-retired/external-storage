# nfs-provisioner
[![Build Status](https://travis-ci.org/kubernetes-incubator/nfs-provisioner.svg?branch=master)](https://travis-ci.org/kubernetes-incubator/nfs-provisioner)

nfs-provisioner is an out-of-tree dynamic provisioner for Kubernetes 1.4. You can use it to quickly & easily deploy shared storage that works almost anywhere. Or it can help you write your own out-of-tree dynamic provisioner by serving as an example implementation of the requirements detailed in [the proposal](https://github.com/kubernetes/kubernetes/pull/30285).

It works just like in-tree dynamic provisioners: a `StorageClass` object can specify an instance of nfs-provisioner to be its `provisioner` like it specifies in-tree provisioners such as GCE or AWS. Then, the instance of nfs-provisioner will watch for `PersistentVolumeClaims` that ask for the `StorageClass` and automatically create NFS-backed `PersistentVolumes` for them. For more information on how dynamic provisioning works, see [the docs](http://kubernetes.io/docs/user-guide/persistent-volumes/) or [this blog post](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html).

## Quickstart
Create a provisioner pod with the name `matthew/nfs`, by specifying the arg "-provisioner=matthew/nfs".
```
$ kubectl create -f deploy/kube-config/pod.yaml
pod "nfs-provisioner" created
```

Create a `StorageClass` named "matthew" with `provisioner: matthew/nfs`.
```
$ kubectl create -f deploy/kube-config/class.yaml
storageclass "matthew" created
```

Create a `PersistentVolumeClaim` with annotation `volume.beta.kubernetes.io/storage-class: "matthew"`
```
$ kubectl create -f deploy/kube-config/claim.yaml
persistentvolumeclaim "nfs" created
```

A `PersistentVolume` is provisioned for the `PersistentVolumeClaim`. Now the claim can be consumed by some pod(s) and the backing NFS storage read from or written to.
```
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM         REASON    AGE
pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           Delete          Bound       default/nfs             23s
```

Deleting the `PersistentVolumeClaim` will cause the provisioner to delete the `PersistentVolume` and its data.

## Running
To deploy nfs-provisioner on a Kubernetes cluster see [Deployment](docs/deployment.md).

To use nfs-provisioner once it is deployed see [Usage](docs/usage.md).

For information on running multiple instances of nfs-provisioner see [Running Multiple Provisioners](docs/multiple.md).

## Implementation 
The controller, the code for which is in the `controller/` directory, watches PVCs and PVs to determine when to provision or delete volumes. It expects to receive an implementation of the `Provisioner` interface which has two methods: `Provision` and `Delete`. This NFS provisioner's implementation of the interface can be found under the `volume/` directory.

So to create your own provisioner, you need to write your own implementation of the interface and pass it to the controller. Ideally you should be able to import the package to create the controller, without modifying any controller code. The passing in of the provisioner to the controller, and initialization of other things they might need (like a client for the Kubernetes API server), is done here in `main.go`.

## Roadmap
This is still alpha/experimental and will change to reflect the [out-of-tree dynamic provisioner proposal](https://github.com/kubernetes/kubernetes/pull/3028)

November
* Create a process for releasing (to Docker Hub, etc.)
* Release 0.1 for kubernetes 1.5
* Support using the controller as a library
* Support running the provisioner as a StatefulSet

December
* Prevent multiple provisioners from racing to provision where possible (in a StatefulSet or DaemonSet)
* Add configurable retries for failed provisioning and deleting

## Community, discussion, contribution, and support

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

You can reach the maintainers of this project at:

- Slack: #sig-storage

## Kubernetes Incubator

This is a [Kubernetes Incubator project](https://github.com/kubernetes/community/blob/master/incubator.md). The project was established 2016-11-15. The incubator team for the project is:

- Sponsor: Clayton (@smarterclayton)
- Champion: Brad (@childsb)
- SIG: sig-storage

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).
