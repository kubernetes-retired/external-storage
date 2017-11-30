Volume Snapshots in Kubernetes
=========================================

This document describes current state of Volume Snapshot support in Kubernetes provided by external controller and provisioner. Familiarity with [Persistent Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/), [Persistent Volume Claims](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#persistentvolumeclaims) and [Dynamic Provisioning](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html) is recommended.

# Introduction

Many storage systems provide the ability to create "snapshots" of a persistent volume to protect against data loss. The external snapshot controller and provisioner provide means to use the feature in Kubernetes cluster and handle the volume snapshots through Kubernetes API.

# Features

* Create snapshot of a `PersistentVolume` bound to a `PersistentVolumeClaim`
* List existing `VolumeSnapshots`
* Delete existing `VolumeSnapshot`
* Create a new `PersistentVolume` from an existing `VolumeSnapshot`
* Supported `PersistentVolume` [types](https://kubernetes.io/docs/concepts/storage/persistent-volumes/#types-of-persistent-volumes):
    * Amazon EBS
    * GCE PD
    * HostPath
    * OpenStack Cinder
    * GlusterFS

# Lifecycle of a Volume Snapshot and Volume Snapshot Data

## Prerequisites
Prerequisites for using the snapshotting features are described in sections "Persistent Volume Claim and Persistent Volume" and "Snapshot Promoter".

### Persistent Volume Claim and Persistent Volume
The user already created a Persistent Volume Claim that is bound to a Persistent Volume. The Persistent Volume type must be one of the snaphot supported Persistent Volume types.

### Snapshot Promoter
An admin created a Storage Class like the one shown below:
```yaml
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: snapshot-promoter
provisioner: volumesnapshot.external-storage.k8s.io/snapshot-promoter
```
Such Storage Class is necessary for restoring a Persistent Volume from already created Volume Snapshot and Volume Snapshot Data.

## Creating Snapshot
Each `VolumeSnapshot` contains a spec and status, which is the specification and status of the Volume Snapshot.
```yaml
apiVersion: volumesnapshot.external-storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: snapshot-demo
spec:
  persistentVolumeClaimName: ebs-pvc
```

* `persistentVolumeClaimName`: name of the Persistent Volume Claim that is bound to a Persistent Volume. This particular Persistent Volume will be snapshotted.

Volume Snapshot Data is automatically created based on the Volume Snapshot. Relationship between Volume Snapshot and Volume Snapshot Data is similar to the relationship between Persistent Volume Claim and Persistent Volume.

Depending on the Persistent Volume type the operation might go through several phases which are reflected by the Volume Snapshot status:

1. The new Volume Snapshot object is created.
2. The controller starts the snapshot operation: the snapshotted Persistent Volume might need to be frozen and the applications paused.
3. The storage system finishes creating the snapshot (the snapshot is "cut") and the snapshotted Persistent Volume might return to normal operation. The snapshot itself is not ready yet. The last status condition is of `Pending` type with status value "True". A new VolumeSnapshotData object is created to represent the actual snapshot.
4. The newly created snapshot is completed and ready to use. The last status condition is of `Ready` type with status value "True"

*Notes*

* It is the user's responsibility to ensure the data consistency (stop the pod/application, flush caches, freeze the filesystem, ...).
* In case of error in any of the steps the Volume Snapshot status is appended with an `Error` condition.

A Volume Snapshot status can be displayed as shown below:
```sh
$ kubectl get volumesnapshot -o yaml
```
```yaml
apiVersion: volumesnapshot.external-storage.k8s.io/v1
  kind: VolumeSnapshot
  metadata:
    clusterName: ""
    creationTimestamp: 2017-09-19T13:58:28Z
    generation: 0
    labels:
      Timestamp: "1505829508178510973"
    name: snapshot-demo
    namespace: default
    resourceVersion: "780"
    selfLink: /apis/volumesnapshot.external-storage.k8s.io/v1/namespaces/default/volumesnapshots/snapshot-demo
    uid: 9cc5da57-9d42-11e7-9b25-90b11c132b3f
  spec:
    persistentVolumeClaimName: pvc-hostpath
    snapshotDataName: k8s-volume-snapshot-9cc8813e-9d42-11e7-8bed-90b11c132b3f
  status:
    conditions:
    - lastTransitionTime: null
      message: Snapshot created successfully
      reason: ""
      status: "True"
      type: Ready
    creationTimestamp: null
```

## Restoring Snapshot
In order to restore a Persistent Volume from a Volume Snapshot a user creates the following Persistent Volume Claim:
```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: snapshot-pv-provisioning-demo
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
spec:
  storageClassName: snapshot-promoter
```
* `annotations`: `snapshot.alpha.kubernetes.io/snapshot`: the name of the Volume Snapshot that will be restored.
* `storageClassName`: Storage Class created by admin for restoring Volume Snapshots.

A Persistent Volume will be created and bound to the Persistent Volume Claim. The process may take several minutes depending on the Persistent Volume Type.

## Deleting Snapshot
A Volume Snapshot `snapshot-demo` can be deleted as shown below:
```
$ kubectl delete volumesnapshot/snapshot-demo
```
The Volume Snapshot Data that are bound to the Volume Snapshot are also automatically deleted.

## Managing Snapshot Users
Depending on the cluster configuration it might be necessary to allow non-admin users to manipulate the VolumeSnapshot objects on the API server. This might be done by creating a ClusterRole bound to a particular user or group.

Assume the user 'alice' needs to be able to work with snapshots in the cluster. The cluster admin needs to define a new ClusterRole.
```yaml
apiVersion: v1
kind: ClusterRole
metatadata:
  name: volumesnapshot-admin
rules:
- apiGroups:
  - "volumesnapshot.external-storage.k8s.io"
  attributeRestrictions: null
  resources:
  - volumesnapshots
  verbs:
  - create
  - delete
  - deletecollection
  - get
  - list
  - patch
  - update
  - watch

```
Now the cluster role has to be bound to the user 'alice' by creating a ClusterRole binding object.
```yaml
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRoleBinding
metadata:
  name: volumesnapsot-admin
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: volumesnapshot-admin
subjects:
- kind: User
  name: alice
```
This is only an example of API access configuration. Note the VolumeSnapshot objects behave just like any other Kubernetes API objects. Please refer to the [API access control documentation](https://kubernetes.io/docs/admin/accessing-the-api/) for complete guide on managing the API RBAC.
