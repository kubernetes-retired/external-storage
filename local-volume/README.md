# Local Persistent Storage User Guide

## Overview

Local persistent volumes allows users to access local storage through the
standard PVC interface in a simple and portable way.  The PV contains node
affinity information that the system uses to schedule pods to the correct
nodes.

An external static provisioner is available to help simplify local storage
management once the local volumes are configured.  Note that the local
storage provisioner is different from most provisioners and does
not support dynamic provisioning yet.  Instead, it requires that administrators
preconfigure the local volumes on each node and mount them under discovery
directories.  The provisioner will manage the volumes under the discovery
directories by creating and cleaning up PersistentVolumes for each volume.

## Configuration Requirements

* The local-volume plugin expects paths to be stable, including across
  reboots and when disks are added or removed.
* The static provisioner only discovers mount points.  For directory-based local
  volumes, they must be bind-mounted into the discovery directories.

## K8s Feature Status

Also see [known issues](KNOWN_ISSUES.md) and [provisioner CHANGELOG](provisioner/CHANGELOG.md).

### 1.9: Alpha

* New StorageClass `volumeBindingMode` parameter that will delay PVC binding
  until a pod is scheduled.

### 1.7: Alpha

* New `local` PersistentVolume source that allows specifying a directory or mount
  point with node affinity.
* Pod using the PVC that is bound to this PV will always get scheduled to that node.

### Future features

* Pod accessing local raw block device
* Local block devices as a volume source, with partitioning and fs formatting
* Dynamic provisioning for shared local persistent storage
* Local PV health monitoring, taints and tolerations
* Inline PV (use dedicated local disk as ephemeral storage)

## User Guide

These instructions reflect the latest version of the codebase.  For instructions
on older versions, please see version links in the [CHANGELOG](provisioner/CHANGELOG.md).

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

GCE clusters brought up with kube-up.sh will automatically format and mount the
requested Local SSDs, so you can deploy the provisioner with the pre-generated
deployment spec and skip to [step 4](#step-4-create-local-persistent-volume-claim),
unless you want to customize the provisioner spec or storage classes.

##### Pre-1.9

``` console
$ NODE_LOCAL_SSDS=<n> cluster/kube-up.sh
$ kubectl create -f provisioner/deployment/kubernetes/admin_account.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/provisioner_generated_gce_ssd_count.yaml
```

##### 1.9+

``` console
$ NODE_LOCAL_SSDS_EXT=<n>,<scsi|nvme>,fs cluster/kube-up.sh
$ kubectl create -f provisioner/deployment/kubernetes/admin_account.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/class-local-ssds.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/provisioner_generated_gce_ssd_volumes.yaml
```

#### Option 2: GKE

GKE clusters will automatically format and mount the
requested Local SSDs, so you can deploy the provisioner with the pre-generated
deployment spec and skip to [step 4](#step-4-create-local-persistent-volume-claim),
unless you want to customize the provisioner spec or storage classes.

##### Using local-ssd-count option
``` console
$ gcloud container cluster create ... --local-ssd-count=<n> --enable-kubernetes-alpha
$ gcloud container node-pools create ... --local-ssd-count=<n>
$ kubectl create -f provisioner/deployment/kubernetes/admin_account.yaml

# If running K8s 1.9+, also create the StorageClasses
$ kubectl create -f provisioner/deployment/kubernetes/gce/class-local-ssds.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/provisioner_generated_gce_ssd_count.yaml
```

##### Using local-ssd-volumes option (available via whitelist only)
``` console
$ gcloud alpha container cluster create ... --local-ssd-volumes="count=<n>,type=<scsi|nvme>,format=fs" --enable-kubernetes-alpha
$ gcloud alpha container node-pools create ... --local-ssd-volumes="count=<n>,type=<scsi|nvme>,format=fs"
$ kubectl create -f provisioner/deployment/kubernetes/admin_account.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/class-local-ssds.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/provisioner_generated_gce_ssd_volumes.yaml
```

#### Option 3: Baremetal environments

1. Partition and format the disks on each node according to your application's
   requirements.
2. Mount all the filesystems under one directory per StorageClass. The directories
   are specified in a configmap, see below.
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
$ kubectl create -f provisioner/deployment/kubernetes/example-storageclass.yaml
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

In order to generate the environment specific provisioner's spec, **--set engine={gcepre19,gcepost19,gke,baremetal}** parameter
can be used in helm template command. Example for GKE environment, the command line will look like:

``` console
helm template ./helm/provisioner --set engine=gke > ./provisioner/deployment/kubernetes/provisioner_generated.yaml
```
Parameter **--set engine=** canbe used in conjunction with custom vlues.yaml file in the same command line.

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
 
### Step 4: Create local persistent volume claim

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
