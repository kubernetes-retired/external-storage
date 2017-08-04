# Local Persistent Storage User Guide

## Overview

Local persistent volumes allows users to access local storage through the
standard PVC interface in a simple and portable way.  The PV contains node
affinity information that the system uses to schedule pods to the correct
nodes.

An external static provisioner and a related bootstrapper are available to help
simplify local storage management once the local volumes are configured.

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
        * Run a workaround controller that unbinds PVCs for pods that are
          stuck pending. TODO: add link
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
    path: /mnt/disks/ssd1
```
Please replace the following elements to reflect your configuration:
  * "my-node" with the name of kubernetes node which is hosting this
    local storage disk
  * "5Gi" with the required size of storage volume, same as specified in PVC
  * "local-storage" with the name of storage class which should be used
     for local volumes
  * "/mnt/disks/ssd1" with the path to the mount point of local volumes
 
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
  * "local-storage" with the name of storage class which should be used
     for local PVs

## E2E Tests

### Running
``` console
go run hack/e2e.go -- -v --test --test_args="--ginkgo.focus=\[Feature:LocalPersistentVolumes\]"
```

### View CI Results
[GCE Alpha](https://k8s-testgrid.appspot.com/sig-storage#gce-alpha)
[GCE GCI Alpha](https://k8s-testgrid.appspot.com/sig-storage#gci-gce-alpha)

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
