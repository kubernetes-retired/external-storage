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

Current status: 1.7 & 1.8 - Alpha

What works:
* Create a PV specifying a directory with node affinity.
* Pod using the PVC that is bound to this PV will always get scheduled to that node.
* External static provisioner daemonset that discovers local directories,
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
* External provisioner cannot correctly detect capacity of mounts added after it
  has been started.
    * This requires mount propagation to work, which is targeted for 1.9.
    * Workaround: Before adding any new mount points, stop the daemonset, add
      the new mount points, start the daemonset.

Future features:
* Local block devices as a volume source, with partitioning and fs formatting
* Pod accessing local raw block device
* Local PV health monitoring, taints and tolerations
* Inline PV (use dedicated local disk as ephemeral storage)
* Dynamic provisioning for shared local persistent storage

## User Guide

### Step 1: Bringing up a cluster with local disks

#### Option 1: GCE

``` console
KUBE_FEATURE_GATES="PersistentLocalVolumes=true" NODE_LOCAL_SSDS=<n> cluster/kube-up.sh
```

#### Option 2: GKE

``` console
gcloud container cluster create ... --local-ssd-count=<n> --enable-kubernetes-alpha --cluster-version=1.7.1
gcloud container node-pools create ... --local-ssd-count=<n>
```

#### Option 3: Baremetal environments

1. Partition and format the disks on each node according to your application's
   requirements.
2. Mount all the filesystems under one directory per StorageClass. The directories
   are specified in a configmap, see below. By default, the discovery directory is
   `/mnt/disks` and storage class is `local-storage`.
3. Configure the Kubernetes API Server, controller-manager, scheduler, and all kubelets with the `PersistentLocalVolumes` feature gate.

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
$ ALLOW_PRIVILEGED=true LOG_LEVEL=5 FEATURE_GATES=PersistentLocalVolumes=true hack/local-up-cluster.sh
```

3. Continue with [Creating local persistent volumes](#creating-local-persistent-volumes)
   below.

### Step 2: Creating local persistent volumes

#### Option 1: Bootstrapping the external static provisioner

This is optional, only for automated creation and cleanup of local volumes. See
[bootstrapper/](./bootstrapper) and [provisioner/](./provisioner) for details and
sample configuration files.

1. Create an admin account with cluster admin priviledge:
``` console
$ kubectl create -f bootstrapper/deployment/kubernetes/admin-account.yaml
```

2. Create a ConfigMap with your local storage configuration details:
```console
$ kubectl create -f bootstrapper/deployment/kubernetes/example-config.yaml
```

3. Launch the bootstrapper, which in turn creates static provisioner daemonset:
``` console
$ kubectl create -f bootstrapper/deployment/kubernetes/bootstrapper.yaml
```

The bootstrapper launches the external static provisioner, that discovers and creates local-volume PVs.

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
[GCE GCI Alpha](https://k8s-testgrid.appspot.com/sig-storage#gci-gce-alpha)


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
