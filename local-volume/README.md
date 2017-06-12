# Local Persistent Storage User Guide
## Overview
Local persistent volumes allows users to access local storage through the
standard PVC interface in a simple and portable way.  The PV contains node
affinity information that the system uses to schedule pods to the correct
nodes.

An external static provisioner is available to help simplify local storage
management once the local volumes are configured.

## Feature Status
Current status: 1.7 - Alpha

What works:
* Create a PV specifying a directory with node affinity.
* Pod using the PVC that is bound to this PV will always get scheduled to that node.
* External static provisioner daemonset that discovers local directories,
  creates, cleans up and deletes PVs.

What doesn't work and workarounds:
* Multiple local PVCs in a single pod.
    * Goal for 1.8.
    * No known workarounds.
* PVC binding does not consider pod scheduling requirements and may make
  suboptimal or incorrect decisions.
    * Goal for 1.8.
    * Workarounds:
        * Run your pods that require local storage first.
        * Give your pods high priority.
        * Run this workaround controller that unbinds PVCs for pods that are
          stuck pending. LINK
* External provisioner cannot correctly detect capacity of mounts added after it
  has been started.
    * This requires mount propagation to work, which is targeted for 1.8.
    * Workaround: Before adding any new mount points, stop the daemonset, add
      the new mount points, start the daemonset.
* Fsgroup conflict if multiple pods using the same PVC specify different fsgroup
    * Workaround: Don't do this!

Future features:
* Local block devices as a volume source, with partitioning and fs formatting
* Pod accessing local raw block device
* Local PV health monitoring, taints and tolerations
* Inline PV (use dedicated local disk as ephemeral storage)
* Dynamic provisioning for shared local persistent storage

## User Guide
### Bringing up a cluster with local disks
#### GCE
``` console
KUBE_FEATURE_GATES=PersistentLocalVolumes NODE_LOCAL_SSDS=<n> kube-up.sh
```
#### GKE (not available until 1.7)
``` console
gcloud alpha container cluster create ... --local-ssd-count=<n>
gcloud alpha container node-pools create ... --local-ssd-count=<n>
```

#### Baremetal environments
1. Partition and format the disks on each node according to your application's requirements.
2. Mount all the filesystems under one directory per StorageClass.
3. Configure a Kubernetes cluster with the `PersistentLocalVolumes` feature gate.

### Running the external static provisioner
This is optional, only for automated creation and cleanup of local volumes.
See `provisioner/` for details and sample configuration files.

1. Create an admin account with persistentvolume provisioner and node system privileges.
``` console
$ kubectl create -f provisioner/deployment/kubernetes/admin_account.yaml
```
2. Create a ConfigMap with your local storage configuration details.
The default StorageClass is `local-storage`.
TODO: TBD

3. Launch the DaemonSet
``` console
$ kubectl create -f provisioner/deployment/kubernetes/provisioner-daemonset.yaml
```

### Specifying local PV
If you don't use the external provisioner, then you have to create the local PVs
manually. Example PV:

``` yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: example-local-pv
  annotations:
        "volume.alpha.kubernetes.io/node-affinity": `{
            "requiredDuringSchedulingIgnoredDuringExecution": {
                "nodeSelectorTerms": [
                    { "matchExpressions": [
                        { "key": "kubernetes.io/hostname",
                          "operator": "In",
                          "values": ["my-node"]
                        }
                    ]}
                 ]}
              }`,
spec:
    capacity:
      storage: 5Gi
    accessModes:
    - ReadWriteOnce
    persistentVolumeReclaimPolicy: Delete
    storageClassName: local-storage
    local:
      path: /mnt/disks/ssd1
```

### Specifying local PVC
In the PVC, specify the StorageClass of your local PVs.

``` yaml
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: example-local-claim
spec:
  accessModes:
  - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
  storageClassName: local-storage
```

## Best Practices
* For IO isolation, a whole disk per volume is recommended
* For capacity isolation, separate partitions per volume is recommended
* Avoid recreating nodes with the same node name while there are still old PVs
  with that node's affinity specified. Otherwise, the system could think that
  the new node contains the old PVs.

### Deleting/removing the underlying volume
When you want to decommission the local volume, here is a possible workflow.
1. Stop the pods that are using the volume
2. Remove the local volume from the node (ie unmounting, pulling out the disk, etc)
3. Delete the PVC
4. The provisioner will try to cleanup the volume, but will fail since the volume no longer exists
5. Manually delete the PV object

