## Quick Howto

#### NOTE: GlusterFS snapshot is in experimental mode. 

#### Start Snapshot Controller

(assuming you have a running Kubernetes local cluster):
```
_output/bin/snapshot-controller  -kubeconfig=${HOME}/.kube/config
```


#### Start Snapshot PV Provisioner

Unlike exiting PV provisioners that provision blank volume, Snapshot based PV provisioners create volumes based on existing snapshots. Thus new provisioners are needed.

There is a special annotation give to PVCs that request snapshot based PVs. As illustrated below, `snapshot.alpha.kubernetes.io` must point to an existing VolumeSnapshot Object
```yaml
metadata:
  name:
  namespace:
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
```


* Start provisioner (assuming running Kubernetes local cluster):
    ```bash
    _output/bin/snapshot-provisioner  -kubeconfig=${HOME}/.kube/config
    ```


Prepare a PV to take snapshot. You can either use glusterfs dynamic provisioned PVs or static PVs.

```

[root@localhost demo]# kubectl get pvc
NAME      STATUS    VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS   AGE
claim11   Bound     pvc-cf1dab46-d4da-11e7-b711-c85b7636c232   3G         ROX            slow           40s
```

Once you have a PV bound to a PVC, create a storageclass for snapshot.

```

[root@localhost demo]# cat snapshot_sc.yaml 
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: snapshot-promoter
provisioner: volumesnapshot.external-storage.k8s.io/snapshot-promoter
[root@localhost demo]# 

[root@localhost demo]# kubectl create -f snapshot_sc.yaml 
storageclass "snapshot-promoter" created

```

Create a volumesnapshot object:

```

[root@localhost demo]# cat volumesnapshot.yaml
apiVersion: volumesnapshot.external-storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: snapshot-demo
spec:
  persistentVolumeClaimName: claim11
[root@localhost demo]# 

[root@localhost demo]# kubectl create -f volumesnapshot.yaml 
volumesnapshot "snapshot-demo" created
```

As soon as you created a volumesnapshot object you can see a snapshot is created for PV.

```
[root@localhost demo]# 
[root@localhost demo]# kubectl get volumesnapshot -o yaml
apiVersion: v1
items:
- apiVersion: volumesnapshot.external-storage.k8s.io/v1
  kind: VolumeSnapshot
  metadata:
    clusterName: ""
    creationTimestamp: 2017-11-29T07:56:46Z
    generation: 0
    labels:
      SnapshotMetadata-PVName: pvc-cf1dab46-d4da-11e7-b711-c85b7636c232
      SnapshotMetadata-Timestamp: "1511942207069268076"
    name: snapshot-demo
    namespace: default
    resourceVersion: "444"
    selfLink: /apis/volumesnapshot.external-storage.k8s.io/v1/namespaces/default/volumesnapshots/snapshot-demo
    uid: d92f2a00-d4da-11e7-b711-c85b7636c232
  spec:
    persistentVolumeClaimName: claim11
    snapshotDataName: k8s-volume-snapshot-d98a2f0f-d4da-11e7-8881-c85b7636c232
  status:
    conditions:
    - lastTransitionTime: 2017-11-29T07:56:47Z
      message: Snapshot created successfully
      reason: ""
      status: "True"
      type: Ready
    creationTimestamp: null
kind: List
metadata:
  resourceVersion: ""
  selfLink: ""

```
Promote this snap to a PV by creating a PVC as shown below:


```
[root@localhost demo]# cat snapshot-pvc-restore.yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: snapshot-pv-provisioning-demo
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
spec:
  storageClassName: snapshot-promoter
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi


[root@localhost demo]# kubectl create -f snapshot-pvc-restore.yaml 
persistentvolumeclaim "snapshot-pv-provisioning-demo" created

```

Get information about the PVC:

```
[root@localhost demo]# kubectl get pvc
NAME                            STATUS    VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS        AGE
claim11                         Bound     pvc-cf1dab46-d4da-11e7-b711-c85b7636c232   3G         ROX            slow                2m
snapshot-pv-provisioning-demo   Bound     pvc-04879b03-d4db-11e7-b711-c85b7636c232   5Gi        RWO            snapshot-promoter   1m
[root@localhost demo]# 


```

Delete the Snapshot:

```
[root@localhost demo]# kubectl delete volumesnapshot/snapshot-demo
volumesnapshot "snapshot-demo" deleted

```

Verify the volumesnapshot object:

```
[root@localhost demo]# kubectl get volumesnapshot -o yaml
apiVersion: v1
items: []
kind: List
metadata:
  resourceVersion: ""
  selfLink: ""
```

