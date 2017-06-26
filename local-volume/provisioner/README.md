# local-volume-provisioner

`quay.io/external-storage/local-volume-provisioner:1.0.0`

local-volume-provisioner is an out-of-tree static provisioner for the local volume
plugin, which is a 1.7 alpha feature.

It runs on each node in the cluster and monitors specified directories to look for
new local file-based volumes.  The volumes can be a mount point or a directory in
a shared filesystem.  It then statically creates a Local PersistentVolume for each
local volume.  It also monitors when the PersistentVolumes have been released, and
will clean up the volume, and recreate the PV.

## [Changelog](CHANGELOG.md)

## Development

Compile the provisioner
``` console
make
```

Make the container image and push to the registry
``` console
make push
```

## Design

There is one provisioner instance on each node in the cluster.  Each instance is
reponsible for monitoring and managing the local volumes on its node.

The basic components of the provisioner are as follows:

- Discovery: The discovery routine periodically reads the configured discovery
  directories and looks for new mount points that don't have a PV, and creates
  a PV for it.

- Deleter: The deleter routine is invoked by the Informer when a PV phase changes.
  If the phase is Released, then it cleans up the volume and deletes the PV API
  object.

- Cache: A central cache stores all the Local PersistentVolumes that the provisioner
  has created.  It is populated by a PV informer that filters out the PVs that
  belong to this node and have been created by this provisioner.  It is used by
  the Discovery and Deleter routines to get the existing PVs.

- Controller: The controller runs a sync loop that coordinates the other components.
  The discovery and deleter run serially to simplify synchronization with the cache
  and create/delete operations.
