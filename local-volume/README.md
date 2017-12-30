# Local Persistent Storage User Guide

## Overview

Local persistent volumes allows users to access local storage through the
standard PVC interface in a simple and portable way.  The PV contains node
affinity information that the system uses to schedule pods to the correct
nodes.

An external static provisioner and a related bootstrapper are available to help
simplify local storage management once the local volumes are configured.  Note
that the local storage provisioner is different from most provisioners and does
not support dynamic provisioning.  Instead, it requires that administrators
preconfigure the local volumes on each node and mount them under discovery
directories.  The provisioner will manage the volumes under the discovery
directories by creating and cleaning up PersistentVolumes for each volume.

## Feature Status

### 1.9: Alpha

**Important:** Both `PersistentLocalVolumes` and `VolumeScheduling` [feature gates
must be enabled starting in 1.9](#enabling-the-alpha-feature-gates).

What works:
* Everything in 1.7.
* New StorageClass `volumeBindingMode` parameter that fixes the previous
  issues:
    * Multiple local PVCs in a single pod.
    * PVC binding is delayed until pod scheduling and takes into account all the
      pod's scheduling requirements.

What doesn't work:
* If you prebind a PVC (by setting PVC.VolumeName) at the same time that another
Pod is being scheduled, it's possible that the Pod's PVCs will encounter a partial
binding failure.  Manual recovery is needed in this situation.
    * Workarounds:
         * Don't prebind PVCs and have Kubernetes bind volumes for the same
           StorageClass.
         * Prebind PV upon creation instead.

### 1.7: Alpha

What works:
* Create a PV specifying a directory with node affinity.
* Pod using the PVC that is bound to this PV will always get scheduled to that node.
* External static provisioner DaemonSet that discovers local directories,
  creates, cleans up and deletes PVs.

What doesn't work and workarounds:
* Multiple local PVCs in a single pod.
    * Goal for 1.9.
    * No known workarounds.
* PVC binding does not consider pod scheduling requirements and may make
  suboptimal or incorrect decisions.
    * Goal for 1.9.
    * Workarounds:
        * Run your pods that require local storage first.
        * Give your pods high priority.
        * Run a workaround controller that unbinds PVCs for pods that are
          stuck pending. TODO: add link
* The provisioner will not correctly detect mounts added after it
  has been started without mount propogation.
  * This issue is resolved in provisioner 2.0 when the mount propagation
  alpha feature gate is enabled, which is available in Kubernetes 1.8+.
  * The provisioner 1.x will detect the existance of a new directory in the
  discovery directory. Then, it will incorrectly create a local PV with
  the root filesystem capacity. 
  * Provisioner 1.x workaround: Before adding any new mount points, stop
  the provisioner daemonset, add the new mount points, start the daemonset.

### Future features
* Local block devices as a volume source, with partitioning and fs formatting
* Pod accessing local raw block device
* Local PV health monitoring, taints and tolerations
* Inline PV (use dedicated local disk as ephemeral storage)
* Dynamic provisioning for shared local persistent storage

## User Guide

### Step 1: Bringing up a cluster with local disks

#### Enabling the alpha feature gates

##### 1.7
```
$ export KUBE_FEATURE_GATES="PersistentLocalVolumes=true"
```

##### 1.8
```
$ export KUBE_FEATURE_GATES="PersistentLocalVolumes=true,MountPropagation=true"
```

##### 1.9+
```
$ export KUBE_FEATURE_GATES="PersistentLocalVolumes=true,VolumeScheduling=true,MountPropagation=true"
```

#### Option 1: GCE

##### Pre-1.9

``` console
$ NODE_LOCAL_SSDS=<n> cluster/kube-up.sh
# This handles creating the ConfigMap as described below
$ kubectl create -f bootstrapper/deployment/kubernetes/latest/gce/config-local-ssd.yaml
```

##### 1.9+

``` console
$ NODE_LOCAL_SSDS_EXT=<n>,<scsi|nvme>,fs cluster/kube-up.sh
# This handles creating the StorageClasses and ConfigMap as described below
$ kubectl create -f bootstrapper/deployment/kubernetes/latest/gce/config-local-ssd-ext.yaml
$ kubectl create -f bootstrapper/deployment/kubernetes/latest/gce/class-local-ssds.yaml
```

#### Option 2: GKE

``` console
$ gcloud container cluster create ... --local-ssd-count=<n> --enable-kubernetes-alpha
$ gcloud container node-pools create ... --local-ssd-count=<n>
# This handles creating ConfigMap as described below
$ kubectl create -f bootstrapper/deployment/kubernetes/latest/gce/config-local-ssd.yaml
# If running K8s 1.9+, also create the StorageClasses
$ kubectl create -f bootstrapper/deployment/kubernetes/latest/gce/class-local-ssds.yaml
```

#### Option 3: Baremetal environments

1. Partition and format the disks on each node according to your application's
   requirements.
2. Mount all the filesystems under one directory per StorageClass. The directories
   are specified in a ConfigMap, see below. By default, the discovery directory is
   `/mnt/disks` and storage class is `local-storage`.
3. Configure the Kubernetes API Server, controller-manager, scheduler, and all kubelets
   with `KUBE_FEATURE_GATES` as described [above](#enabling-the-alpha-feature-gates).
4. If not using the default Kubernetes scheduler policy, the following
   predicates must be enabled:
   * Pre-1.9: `NoVolumeBindConflict`
   * 1.9+: `VolumeBindingChecker`

#### Option 4: Local test cluster

1. Create `/mnt/disks` directory and mount several volumes into its subdirectories.
   The example below uses three ram disks to simulate real local volumes:
```console
$ mkdir /mnt/disks
$ for vol in vol1 vol2 vol3; do
    mkdir /mnt/disks/$vol
    mount -t tmpfs $vol /mnt/disks/$vol
done
```

2. Run the local cluster.

```console
$ ALLOW_PRIVILEGED=true LOG_LEVEL=5 FEATURE_GATES=$KUBE_FEATURE_GATES hack/local-up-cluster.sh
```

### Step 2: Creating a StorageClass (1.9+)
To delay volume binding until pod scheduling and to handle multiple local PVs in
a single pod, a StorageClass must to be created with `volumeBindingMode` set to
`WaitForFirstConsumer`.

```console
$ kubectl create -f bootstrapper/deployment/kubernetes/example-storageclass.yaml
```

### Step 3: Creating local persistent volumes

#### Option 1: Using the local volume static provisioner 

	1. Create an admin account with cluster admin privilege:
``` console
$ kubectl create -f ./provisioner/deployment/kubernetes/admin_account.yaml  
```
	2. Generate Provisioner's DaemonSet and ConfigMap spec, and customize it.
This step uses helm templates to generate the specs.  See the [helm README](helm) for setup instructions.
To generate the provisioner's specs using the [default values](helm/provisioner/values.yaml), run:

``` console
helm template ./helm/provisioner > ./provisioner/deployment/kubernetes/provisioner_generated.yaml 
```

You can also provide a custom values file instead:

``` console
helm template ./helm/provisioner --values custom-values.yaml > ./provisioner/deployment/kubernetes/provisioner_generated.yaml
```
	3. Deploy Provisioner 
Once a user is satisfied with the content of Provisioner's yaml file, **kubectl** can be used
to create Provisioner's DaemonSet and ConfigMap.
 
``` console
$ kubectl create -f ./provisioner/deployment/kubernetes/provisioner_generated.yaml 
```
	4. Check discovered local volumes
Once launched, the external static provisioner will discover and create local-volume PVs.

For example, if the directory `/mnt/disks/` contained one directory `/mnt/disks/vol1` then the following
local-volume PV would be created by the static provisioner:

```
$ kubectl get pv
NAME                CAPACITY    ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM     STORAGECLASS    REASON    AGE
local-pv-ce05be60   1024220Ki   RWO           Delete          Available             local-storage             26s

$ kubectl describe pv local-pv-ce05be60 
Name:		local-pv-ce05be60
Labels:		<none>
Annotations:	pv.kubernetes.io/provisioned-by=local-volume-provisioner-minikube-18f57fb2-a186-11e7-b543-080027d51893
		volume.alpha.kubernetes.io/node-affinity={"requiredDuringSchedulingIgnoredDuringExecution":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kubernetes.io/hostname","operator":"In","values":["minikub...
StorageClass:	local-fast
Status:		Available
Claim:		
Reclaim Policy:	Delete
Access Modes:	RWO
Capacity:	1024220Ki
Message:	
Source:
    Type:	LocalVolume (a persistent volume backed by local storage on a node)
    Path:	/mnt/disks/vol1
Events:		<none>

```

The PV described above can be claimed and bound to a PVC by referencing the `local-fast` storageClassName.

#### Option 2: Manually create local persistent volume

If you don't use the external provisioner, then you have to create the local PVs
manually. Note that with manual PV creation, the volume has to be manually
reclaimed when deleted. Example PV:

``` yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: example-local-pv
  annotations:
    "volume.alpha.kubernetes.io/node-affinity": '{
      "requiredDuringSchedulingIgnoredDuringExecution": {
        "nodeSelectorTerms": [
          { "matchExpressions": [
            { "key": "kubernetes.io/hostname",
              "operator": "In",
              "values": ["my-node"]
            }
          ]}
         ]}
        }'
spec:
  capacity:
    storage: 5Gi
  accessModes:
  - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: local-storage
  local:
    path: /mnt/disks/vol1
```
Please replace the following elements to reflect your configuration:

  * "my-node" with the name of kubernetes node that is hosting this
    local storage disk
  * "5Gi" with the required size of storage volume, same as specified in PVC
  * "local-storage" with the name of storage class to associate with
     this local volume
  * "/mnt/disks/vol1" with the path to the mount point of local volumes
 
### Step 3: Create local persistent volume claim

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
Please replace the following elements to reflect your configuration:

  * "5Gi" with required size of storage volume
  * "local-storage" with the name of storage class associated with the
  local PVs that should be used for satisfying this PVC

## E2E Tests

### Running
``` console
go run hack/e2e.go -- -v --test --test_args="--ginkgo.focus=\[Feature:LocalPersistentVolumes\]"
```

### View CI Results
[GCE Alpha](https://k8s-testgrid.appspot.com/sig-storage#gce-alpha)

[GKE Alpha](https://k8s-testgrid.appspot.com/sig-storage#gke-alpha)


## Requirements

* The local-volume plugin expects paths to be stable, including across 
  reboots and when disks are added or removed.

## Best Practices

* For IO isolation, a whole disk per volume is recommended
* For capacity isolation, separate partitions per volume is recommended
* Avoid recreating nodes with the same node name while there are still old PVs
  with that node's affinity specified. Otherwise, the system could think that
  the new node contains the old PVs.
* For volumes with a filesystem, it's recommended to utilize their UUID (e.g.
  the output from `ls -l /dev/disk/by-uuid`) both in fstab entries
  and in the directory name of that mount point. This practice ensures
  that the wrong local volume is not mistakenly mounted, even if its device path
  changes (e.g. if /dev/sda1 becomes /dev/sdb1 when a new disk is added).
  Additionally, this practice will ensure that if another node with the
  same name is created, that any volumes on that node are unique and not
  mistaken for a volume on another node with the same name.
* For raw block volumes without a filesystem, use a unique ID as the symlink
  name. Depending on your environment, the volume's ID in `/dev/disk/by-id/`
  may contain a unique hardware serial number. Otherwise, a unique ID should be 
  generated. The uniqueness of the symlink name will ensure that if another 
  node with the same name is created, that any volumes on that node are 
  unique and not mistaken for a volume on another node with the same name.


### Deleting/removing the underlying volume

When you want to decommission the local volume, here is a possible workflow.
1. Stop the pods that are using the volume
2. Remove the local volume from the node (ie unmounting, pulling out the disk, etc)
3. Delete the PVC
4. The provisioner will try to cleanup the volume, but will fail since the volume no longer exists
5. Manually delete the PV object
