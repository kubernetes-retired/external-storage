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
preconfigure the local volumes on each node and if volumes are supposed to be

 1. Filesystem volumeMode (default) PVs - mount them under discovery directories.
 2. Block volumeMode PVs - create a symbolic link under discovery directory to
    the block device on the node.

The provisioner will manage the volumes under the discovery directories by creating
and cleaning up PersistentVolumes for each volume.

## Configuration Requirements

* The local-volume plugin expects paths to be stable, including across
  reboots and when disks are added or removed.
* The static provisioner only discovers either mount points (for Filesystem mode volumes)
  or symbolic links (for Block mode volumes). For directory-based local volumes, they
  must be bind-mounted into the discovery directories.

## Version Compatibility

Recommended provisioner versions with Kubernetes versions

| Provisioner version | K8s version   | Reason                    |
| ------------------- | ------------- | ------------------------- |
| [2.1.0][3]          | 1.10          | Beta API default, block   |
| [2.0.0][2]          | 1.8, 1.9      | Mount propagation         |
| [1.0.1][1]          | 1.7           |                           |

[1]: https://github.com/kubernetes-incubator/external-storage/tree/local-volume-provisioner-v1.0.1/local-volume
[2]: https://github.com/kubernetes-incubator/external-storage/tree/local-volume-provisioner-v2.0.0/local-volume
[3]: https://github.com/kubernetes-incubator/external-storage/tree/local-volume-provisioner-v2.1.0/local-volume

## K8s Feature Status

Also see [known issues](KNOWN_ISSUES.md) and [CHANGELOG](CHANGELOG.md).

### 1.10: Beta

* New PV.NodeAffinity field added.
* **Important:** Alpha PV NodeAffinity annotation is deprecated. Users must manually update
  their PVs to use the new NodeAffinity field or run a [one-time update job](utils/update-pv-to-beta).
* Alpha: Raw block support added.

### 1.9: Alpha

* New StorageClass `volumeBindingMode` parameter that will delay PVC binding
  until a pod is scheduled.

### 1.7: Alpha

* New `local` PersistentVolume source that allows specifying a directory or mount
  point with node affinity.
* Pod using the PVC that is bound to this PV will always get scheduled to that node.

### Future features

* Local block devices as a volume source, with partitioning and fs formatting
* Dynamic provisioning for shared local persistent storage
* Local PV health monitoring, taints and tolerations
* Inline PV (use dedicated local disk as ephemeral storage)

## User Guide

These instructions reflect the latest version of the codebase.  For instructions
on older versions, please see version links under
[Version Compatibility](#version-compatibility).

### Step 1: Bringing up a cluster with local disks

#### Enabling the alpha feature gates

##### 1.10+

If raw local block feature is needed,
```
$ export KUBE_FEATURE_GATES="BlockVolume=true"
```

Note: Kubernetes versions prior to 1.10 require [several additional
feature-gates](https://github.com/kubernetes-incubator/external-storage/tree/local-volume-provisioner-v2.0.0/local-volume#enabling-the-alpha-feature-gates) 
be enabled on all Kubernetes components, because the persistent lcoal volumes and other features were in alpha.

#### Option 1: GCE

GCE clusters brought up with kube-up.sh will automatically format and mount the
requested Local SSDs, so you can deploy the provisioner with the pre-generated
deployment spec and skip to [step 4](#step-4-create-local-persistent-volume-claim),
unless you want to customize the provisioner spec or storage classes.

``` console
$ NODE_LOCAL_SSDS_EXT=<n>,<scsi|nvme>,fs cluster/kube-up.sh
$ kubectl create -f provisioner/deployment/kubernetes/gce/class-local-ssds.yaml
$ kubectl create -f provisioner/deployment/kubernetes/gce/provisioner_generated_gce_ssd_volumes.yaml
```

#### Option 2: GKE

GKE clusters will automatically format and mount the
requested Local SSDs. Please see
[GKE documentation](https://cloud.google.com/kubernetes-engine/docs/concepts/local-ssd)
for instructions for how to create a cluster with Local SSDs.

Then skip to [step 4](#step-4-create-local-persistent-volume-claim).

**Note:** The raw block feature is only supported on GKE Kubernetes alpha clusters.

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
$ kubectl create -f provisioner/deployment/kubernetes/example/default_example_storageclass.yaml
```

### Step 3: Creating local persistent volumes

#### Option 1: Using the local volume static provisioner

1. Generate Provisioner's ServiceAccount, Roles, DaemonSet, and ConfigMap spec, and customize it.

    This step uses helm templates to generate the specs.  See the [helm README](helm) for setup instructions.
    To generate the provisioner's specs using the [default values](helm/provisioner/values.yaml), run:

    ``` console
    helm template ./helm/provisioner > ./provisioner/deployment/kubernetes/provisioner_generated.yaml
    ```

    You can also provide a custom values file instead:

    ``` console
    helm template ./helm/provisioner --values custom-values.yaml > ./provisioner/deployment/kubernetes/provisioner_generated.yaml
    ```

2. Deploy Provisioner

    Once a user is satisfied with the content of Provisioner's yaml file, **kubectl** can be used
    to create Provisioner's DaemonSet and ConfigMap.

    ``` console
    $ kubectl create -f ./provisioner/deployment/kubernetes/provisioner_generated.yaml
    ```

3. Check discovered local volumes

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
    StorageClass:	local-fast
    Status:		Available
    Claim:		
    Reclaim Policy:	Delete
    Access Modes:	RWO
    Capacity:	1024220Ki
    NodeAffinity:
      Required Terms:
          Term 0:  kubernetes.io/hostname in [my-node]
    Message:	
    Source:
        Type:	LocalVolume (a persistent volume backed by local storage on a node)
        Path:	/mnt/disks/vol1
    Events:		<none>
    ```

    The PV described above can be claimed and bound to a PVC by referencing the `local-fast` storageClassName.

#### Option 2: Manually create local persistent volume

See [Kubernetes documentation](https://kubernetes.io/docs/concepts/storage/volumes/#local)
for an example PersistentVolume spec.

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

For "Block" volumeMode PVC, which tries to claim a "Block" PV, the following
example can be used:

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
  volumeMode: Block
  storageClassName: local-storage
```
Note that the only additional field of interest here is volumeMode, which has been set
to "Block".

## E2E Tests

### Running
``` console
go run hack/e2e.go -- -v --test --test_args="--ginkgo.focus=PersistentVolumes-local"
```

### View CI Results
[GCE](https://k8s-testgrid.appspot.com/sig-storage#gce&include-filter-by-regex=PersistentVolumes-local)

[GKE](https://k8s-testgrid.appspot.com/sig-storage#gke&include-filter-by-regex=PersistentVolumes-local)

[GCE Slow](https://k8s-testgrid.appspot.com/sig-storage#gce-slow&include-filter-by-regex=PersistentVolumes-local)

[GKE Slow](https://k8s-testgrid.appspot.com/sig-storage#gke-slow&include-filter-by-regex=PersistentVolumes-local)

[GCE Serial](https://k8s-testgrid.appspot.com/sig-storage#gce-serial&include-filter-by-regex=PersistentVolumes-local)

[GKE Serial](https://k8s-testgrid.appspot.com/sig-storage#gke-serial&include-filter-by-regex=PersistentVolumes-local)

[GCE Alpha](https://k8s-testgrid.appspot.com/sig-storage#gce-alpha&include-filter-by-regex=PersistentVolumes-local)

[GKE Alpha](https://k8s-testgrid.appspot.com/sig-storage#gke-alpha&include-filter-by-regex=PersistentVolumes-local)


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
