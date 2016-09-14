# nfs-provisioner

Create the `StorageClass` "matthew" with `provisioner: matthew/nfs`
```
$ kubectl create -f class.yaml
storageclass "matthew" created
```

Create the `ConfigMap` "nfs-provisioner-exports" from exports.json containing static exports to provision
```
$ kubectl create cm --from-file=exports.json nfs-provisioner-exports
configmap "nfs-provisioner-exports" created
```

Create the NFS provisioner pod "nfs-provisioner"
```
$ kubectl create -f pod.yaml
pod "nfs-provisioner" created
```

The NFS provisioner provisions the static exports
```
$ kubectl get pv
NAME      CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM     REASON    AGE
home      1Mi        RWX           Retain          Available                       2m
```

Create a PVC "nfs" requesting `StorageClass` "matthew"
```
$ kubectl create -f claim.yaml
persistentvolumeclaim "nfs" created
```

The NFS provisioner provisions a PV for PVC "nfs"
```
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM         REASON    AGE
home                                       1Mi        RWX           Retain          Available                           3m
pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           Delete          Bound       default/nfs             23s
```

The PVC & PV are bound
```
$ kubectl get pvc
NAME      STATUS    VOLUME                                     CAPACITY   ACCESSMODES   AGE
nfs       Bound     pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           2s
```

Create a pod "write-pod" that consumes the claim "nfs" to test writing to the provisioned NFS share
```
$ kubectl create -f write_pod.yaml
pod "write-pod" created
```

The pod "write-pod" successfully writes to the provisioned NFS share
```
$ kubectl get pod
NAME              READY     STATUS      RESTARTS   AGE
nfs-provisioner   1/1       Running     0          5m
write-pod         0/1       Completed   3          47s
```

Delete the PVC "nfs"
```
$ kubectl delete pvc nfs
persistentvolumeclaim "nfs" deleted
```

The NFS provisioner deletes the PV it originally provisioned for PVC "nfs"
```
$ kubectl get pv
NAME      CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM     REASON    AGE
home      1Mi        RWX           Retain          Available                       9m
```
