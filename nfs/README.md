# nfs-provisioner

[![Docker Repository on Quay](https://quay.io/repository/kubernetes_incubator/nfs-provisioner/status "Docker Repository on Quay")](https://quay.io/repository/kubernetes_incubator/nfs-provisioner)
```
quay.io/kubernetes_incubator/nfs-provisioner
```

nfs-provisioner is an out-of-tree dynamic provisioner for Kubernetes 1.4+. You can use it to quickly & easily deploy shared storage that works almost anywhere. Or it can help you write your own out-of-tree dynamic provisioner by serving as an example implementation of the requirements detailed in [the proposal](https://github.com/kubernetes/kubernetes/pull/30285). Go [here](./docs/demo) for a demo of how to use it and [here](../docs/demo/hostpath-provisioner) for an example of how to write your own.

It works just like in-tree dynamic provisioners: a `StorageClass` object can specify an instance of nfs-provisioner to be its `provisioner` like it specifies in-tree provisioners such as GCE or AWS. Then, the instance of nfs-provisioner will watch for `PersistentVolumeClaims` that ask for the `StorageClass` and automatically create NFS-backed `PersistentVolumes` for them. For more information on how dynamic provisioning works, see [the docs](http://kubernetes.io/docs/user-guide/persistent-volumes/) or [this blog post](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html).

## Quickstart
Choose some volume for your nfs-provisioner instance to store its state & data in and mount the volume at `/export` in `deploy/kubernetes/deployment.yaml`. It doesn't have to be a `hostPath` volume, it can e.g. be a PVC. Note that the volume must have a [supported file system](https://github.com/nfs-ganesha/nfs-ganesha/wiki/Fsalsupport#vfs) on it: any local filesystem on Linux is supported & NFS is not supported.
```yaml
...
  volumeMounts:
    - name: export-volume
      mountPath: /export
volumes:
  - name: export-volume
    hostPath:
      path: /tmp/nfs-provisioner
...
```

Choose a `provisioner` name for a `StorageClass` to specify and set it in `deploy/kubernetes/deployment.yaml`
```yaml
...
args:
  - "-provisioner=example.com/nfs"
...
```

Create the deployment.
```console
$ kubectl create -f deploy/kubernetes/deployment.yaml
service "nfs-provisioner" created
deployment "nfs-provisioner" created
```

Create a `StorageClass` named "example-nfs" with `provisioner: example.com/nfs`.
```console
$ kubectl create -f deploy/kubernetes/class.yaml
storageclass "example-nfs" created
```

Create a `PersistentVolumeClaim` with annotation `volume.beta.kubernetes.io/storage-class: "example-nfs"`
```console
$ kubectl create -f deploy/kubernetes/claim.yaml
persistentvolumeclaim "nfs" created
```

A `PersistentVolume` is provisioned for the `PersistentVolumeClaim`. Now the claim can be consumed by some pod(s) and the backing NFS storage read from or written to.
```console
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM         REASON    AGE
pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           Delete          Bound       default/nfs             23s
```

Deleting the `PersistentVolumeClaim` will cause the provisioner to delete the `PersistentVolume` and its data.

Deleting the provisioner deployment will cause any outstanding `PersistentVolumes` to become unusable for as long as the provisioner is gone.

## Running

To deploy nfs-provisioner on a Kubernetes cluster see [Deployment](docs/deployment.md).

To use nfs-provisioner once it is deployed see [Usage](docs/usage.md).

## [Changelog](CHANGELOG.md)
Releases done here in external-storage will not have corresponding git tags (external-storage's git tags are reserved for versioning the library), so to keep track of releases check this README, the [changelog](CHANGELOG.md), or [Quay](https://quay.io/repository/kubernetes_incubator/nfs-provisioner)

## Writing your own
Go [here](../docs/demo/hostpath-provisioner) for an example of how to write your own out-of-tree dynamic provisioner.

## Roadmap
This is still alpha/experimental and will change to reflect the [out-of-tree dynamic provisioner proposal](https://github.com/kubernetes/kubernetes/pull/30285)

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
