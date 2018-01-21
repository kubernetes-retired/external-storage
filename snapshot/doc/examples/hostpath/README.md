## Quick Howto


#### Start Snapshot Controller

(assuming running Kubernetes local cluster):
```
_output/bin/snapshot-controller  -kubeconfig=${HOME}/.kube/config
```

#### Prepare a HostPath PV
We use `/tmp/test` in this example as HostPath directory represented by PV.

* Create the directory
    ```bash
    mkdir /tmp/test
    ```

* Create PV for it and PVC that binds to it
    ```bash
    kubectl create -f examples/hostpath/pv.yaml
    kubectl create -f examples/hostpath/pvc.yaml
    ```

* Simulate a pod that writes some data to the HostPath volume. We do not need a real pod, simple `echo` is enough. In real life, there would be a pod creating say MySQL database in the volume.
    ```bash
    echo "hello world" > /tmp/test/data
    ```

####  Create a snapshot
Now we have PVC bound to a PV that contains some data. We want to take snapshot of this data so we can restore the data later.

 * Create a Snapshot Third Party Resource
    ```bash
    kubectl create -f examples/hostpath/snapshot.yaml
    ```

#### Check VolumeSnapshot and VolumeSnapshotData are created

* Appropriate Kubernetes objects are available and describe the snapshot (output is trimmed for readability):
    ```bash
    kubectl get volumesnapshot,volumesnapshotdata -o yaml
    apiVersion: v1
    items:
    - apiVersion: volumesnapshot.external-storage.k8s.io/v1
      kind: VolumeSnapshot
      metadata:
        labels:
          Timestamp: "1505214128800981201"
        name: snapshot-demo
        namespace: default
      spec:
        persistentVolumeClaimName: hostpath-pvc
        snapshotDataName: k8s-volume-snapshot-d2209d5a-97a9-11e7-b963-5254000ac840
      status:
        conditions:
        - lastTransitionTime: 2017-09-12T11:02:08Z
          message: Snapshot created successfully
          status: "True"
          type: Ready
    - apiVersion: volumesnapshot.external-storage.k8s.io/v1
      kind: VolumeSnapshotData
      metadata:
        name: k8s-volume-snapshot-d2209d5a-97a9-11e7-b963-5254000ac840
        namespace: ""
      spec:
        hostPath:
          snapshot: /tmp/d21782d1-97a9-11e7-b963-5254000ac840.tgz
        persistentVolumeRef:
          kind: PersistentVolume
          name: hostpath-pv
        volumeSnapshotRef:
          kind: VolumeSnapshot
          name: default/snapshot-demo
      status:
        conditions:
          message: Snapshot created successfully
          status: "True"
          type: Ready
    kind: List
    metadata: {}
    resourceVersion: ""
    selfLink: ""
    ```

* The snapshot is available on disk as `.tgz` archive as specified in volumeSnapshotData.spec.hostPath.snapshot. The file contains snapshot of our `/tmp/test` directory:
    ```bash
    $ tar tzf /tmp/d21782d1-97a9-11e7-b963-5254000ac840.tgz
    test/
    test/data
    ```

## Snapshot based PV Provisioner

Unlike exiting PV provisioners that provision blank volume, Snapshot based PV provisioners create volumes based on existing snapshots. Thus new provisioners are needed.

There is a special annotation give to PVCs that request snapshot based PVs. As illustrated in [the example](examples/hostpath/claim.yaml), `snapshot.alpha.kubernetes.io` must point to an existing VolumeSnapshot Object
```yaml
metadata:
  name:
  namespace:
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
```

## HostPath Volume Type

### Start PV Provisioner and Storage Class to restore a snapshot to a PV

* Start provisioner (assuming running Kubernetes local cluster):
    ```bash
    _output/bin/snapshot-provisioner  -kubeconfig=${HOME}/.kube/config
    ```

* Create a storage class:
    ```bash
    kubectl create -f examples/hostpath/class.yaml
    ```

### Restore a snapshot to a new PV

* Create a PVC that claims a PV based on an existing snapshot
    ```bash
    kubectl create -f examples/hostpath/claim.yaml
    ```
* Check that a PV was created

    ```bash
    kubectl get pv,pvc
    ```

Snapshots are restored to `/tmp/restore`.
