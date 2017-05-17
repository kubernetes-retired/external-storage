# local-volume-provisioner

NOTE: provisioner is still in development and is not functional yet

local-volume-provisioner is an out-of-tree static provisioner for the Local volume plugin, which is a 1.7 alpha feature.

It runs on each node in the cluster and monitors specified directories to look for new local file-based volumes.  The volumes can be a mount point or a directory in a shared filesystem.  It then statically creates a Local PersistentVolume for each local volume.  It also monitors when the PersistentVolumes have been released, and will clean up the volume, and recreate the PV.


## Quickstart
Create an admin account with persistentvolume provisioner privileges.
``` console
$ kubectl create -f deployment/kubernetes/admin_account.yaml
```

Launch the DaemonSet
``` console
$ kubectl create -f deployment/kubernetes/provisioner_daemonset.yaml
```

## Development
Compile the provisioner
``` console
make
```

Make the container image and push to the registry
``` console
make push
```

## Deployment
### Provisioner deployment arguments


### Setting up a cluster with local storage
Bring up a GCE cluster with local SSDs
``` console
NODE_LOCAL_SSDS=3 kube-up.sh
```

## Best Practices
* For IO isolation, a whole disk per volume is recommended
* For capacity isolation, separate partitions per volume is recommended

### Deleting/removing the underlying volume
When you want to decommission the local volume, here is a possible workflow.
1. Stop the pods that are using the volume
2. Remove the local volume from the node (ie unmounting, pulling out the disk, etc)
3. Delete the PVC
4. The provisioner will try to cleanup the volume, but will fail since the volume no longer exists
5. Manually delete the PV object

## Design
There is one provisioner instance on each node in the cluster.  Each instance is reponsible for monitoring and managing the local volumes on its node.

The basic components of the provisioner are as follows:

Discovery: The discovery routine periodically reads the configured discovery directories and looks for new mount points that don't have a PV, and creates a PV for it.  

Deleter: The deleter routine is invoked by the Informer when a PV phase changes.  If the phase is Released, then it cleans up the volume and deletes the PV API object.  

Cache: A central cache stores all the Local PersistentVolumes that the provisioner has created.  It is populated by a PV informer that filters out the PVs that belong to this node and have been created by this provisioner.  It is used by the Discovery and Deleter routines to get the existing PVs.

Controller: The controller runs a sync loop that coordinates the other components.
