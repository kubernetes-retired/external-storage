## Usage

The nfs-provisioner has been deployed and is now watching for claims it should provision volumes for. No such claims can exist until a properly configured `StorageClass` for claims to request is created.

Edit the `provisioner` field in `deploy/kube-config/class.yaml` to be the provisioner's name. Configure the `parameters`.

### Parameters
* `gid`: `"none"` or a [supplemental group](http://kubernetes.io/docs/user-guide/security-context/) like `"1001"`. NFS shares will be created with permissions such that only pods running with the supplemental group can read & write to the share. Or if `"none"`, anybody can write to the share. This will only work in conjunction with the `root-squash` flag set true.  Default (if omitted) `"none"`.

Name the `StorageClass` however you like; the name is how claims will request this class. Create the class.
 
```
$ kubectl create -f deploy/kube-config/class.yaml
storageclass "example-nfs" created
```

Now if everything is working correctly, when you create a claim requesting the class you just created, the provisioner will automatically create a volume.

Edit the `volume.beta.kubernetes.io/storage-class` annotation in `deploy/kube-config/claim.yaml` to be the name of the class. Create the claim.

```
$ kubectl create -f deploy/kube-config/claim.yaml
persistentvolumeclaim "nfs" created
```

The nfs-provisioner provisions a PV for the PVC you just created. Its reclaim policy is Delete, so it and its backing storage will be deleted by the provisioner when the PVC is deleted.

```
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM         REASON    AGE
pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           Delete          Bound       default/nfs             23s
```

A pod can consume the PVC and write to the backing NFS share. Create a pod to test this.

```
$ kubectl create -f deploy/kube-config/write_pod.yaml 
pod "write-pod" created
$ kubectl get pod --show-all
nfs-provisioner   1/1       Running     0          31s
write-pod         0/1       Completed   0          41s
```

Once you are done with the PVC, delete it and the provisioner will delete the PV and its backing storage.

```
$ kubectl delete pod write-pod
pod "write-pod" deleted
$ kubectl delete pvc nfs
persistentvolumeclaim "nfs" deleted
$ kubectl get pv
```

Note that deleting or stopping a provisioner won't delete the `PersistentVolume` objects it created. 

If at any point things don't work correctly, check the provisioner's logs using `kubectl logs` and look for events in the PVs and PVCs using `kubectl describe`.

### Using as default

The provisioner can be used as the default storage provider, meaning claims that don't request a `StorageClass` get volumes provisioned for them by the provisioner by default. To set as the default a `StorageClass` that specifies the provisioner, turn on the `DefaultStorageClass` admission-plugin and add the `storageclass.beta.kubernetes.io/is-default-class` annotation to the class. See http://kubernetes.io/docs/user-guide/persistent-volumes/#class-1 for more information.
