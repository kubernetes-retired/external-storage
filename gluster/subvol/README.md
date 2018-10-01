# Gluster Subvol provisioner

## Summary

Use a Gluster Volume with and expose subdirectories as PVCs. Support for
simplified cloning (based on annotations) is one of the main features.

# Terminology

**subvol**: A PersistentVolumeClaim pointing to a subdirectory on the
            *supervol*.

**supervol**: A PersistentVolumeClaim used for storing the *subvols*.

**SmartCloning**: Cloning of a PersistentVolumeClaim handled by the provisioner
		  and storage backend. Requesting a volume to be cloned is done
                  by passing [Annotations in the PVC](#cloning-by-annotations).

# Features

- dynamically provision a *subvol* through a StorageClass pointing to a *supervol*
- *supervol* in a different namespace than the *subvol*
- create/delete Endpoints for each *subvol*
- mount-options from the *supervol* are copied to the *subvol*
- delete the subdirectory and contents from the *supervol* upon PVC deletion
- clone other *subvol* PVC from the same *supervol*

# Requirements

- a Gluster Volume that can be used for the *supervol*
- mounting of *subvol* PVCs require GlusterFS >= 3.13 (released December 2017)

# Installation

- deploy a `gluster-subvol-provisioner` pod
- create a PVC on a Gluster Volume
- create a StorageClass that points to the provisioner and the PVC

Examples of the above steps are included in this repository. For deployment
with a ServiceAccount to allow the needed permissions, see
[`deploy/gluster-subvol-provisioner-pod.yaml`](deploy/gluster-subvol-provisioner-pod.yaml).
An example of the StorageClass (with *supervol* PVC) is in
[`examples/class.yaml`](examples/class.yaml).

# Limitations and Considerations

Currently this provisioner only supports Gluster PVCs. There are very few
requirements in the code that demand using a Gluster storage environments. With
minimal changes this provisioner can be made to function with other filesystems
that provide RWX capabilities for the *supervol*.

## Advantages

- no Gluster management operations
  - resilent, filesystem operations still function when Gluster servers are down
  - fast, filesystem operations only
- can support *SmartCloning* on filesystem level (using reflink, `copy_file_range()`)
- thin provisioning is automatic (all *subvol* PVCs share the same free space)

## Disadvantages (or TODO)

- no quota support yet, a PVC is not bounded by its requested size
- one Gluster Volume per StorageClass, might get many more StorageClasses
- resizing the Gluster Volume on-demand is possible, but not implemented
- undefined behaviour in case the *supervol* PVC is deleted when *subvol* PVCs
  are provisioned

# Design Notes

# Gluster Dependencies

The dependencies on Gluster are kept to a minimum. Ideally this provisioner can
move towards supporting other network filesystems in the future. At the moment,
the following dependencies are part of the code:

- checks on valid `PersistentVolume.Spec.Glusterfs` object
- mounting the *supervol* with `mount -t glusterfs ...`
- creation of `Endpoints` that the mount-plugin uses

## Provisioner Internals

`mtab` is a map with the mountpoints (key=supervol, value=mountpoint) where the
Gluster Volume is mounted. The Gluster Volumes get mounted under
`<workdir>/<supervol-namespace>/pvc-<supervol-pcv.uid>`

The `PV.Spec.Glusterfs.Path` is used for mounting (by kubelet) of the *subvol*
PVC. This needs to be set to `<supervol>/<namespace>/pvc-<pvc.uid>`.

## Cloning by Annotations

`k8s.io/CloneRequest` can be set to `<namespace>/<pvc.name>` to initiate a
clone from an other PVC on the same *supervol*. When a cloned PVC has been
provisioned, the `k8s.io/CloneOf` annotation has been set to the cloned source.

In case cloning failed, an empty *subvol* PVC will be created, but no
`k8s.io/CloneOf` annotation added.
