# Volume Snapshot Controller

## Status

Pre-alpha

## Demo

 - [SIG PM 06/13/2017](https://youtu.be/7-mBY_ZitS8?t=24m41s)


## Quick Howto

 - [Host Path](doc/examples/hostpath/README.md)

 - [AWS EBS](doc/examples/aws/README.md)

 - [GCE PD](doc/examples/gce/README.md)

## Snapshot Volume Plugin Interface

As illustrated in example plugin [hostPath](pkg/volume/hostpath/processor.go)

### Plugin API

A Volume plugin must provide `RegisterPlugin()` to return plugin struct, `GetPluginName()` to return plugin name, and implement the following interface as illustrated in [hostPath](pkg/volume/hostpath/processor.go)

```go
import (
	"k8s.io/client-go/pkg/api/v1"

	crdv1 "github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1"
	"github.com/kubernetes-incubator/external-storage/snapshot/pkg/cloudprovider"
)

type VolumePlugin interface {
	// Init inits volume plugin
	Init(cloudprovider.Interface)
	// SnapshotCreate creates a VolumeSnapshot from a PersistentVolumeSpec
	SnapshotCreate(*v1.PersistentVolume, *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error)
	// SnapshotDelete deletes a VolumeSnapshot
	// PersistentVolume is provided for volume types, if any, that need PV Spec to delete snapshot
	SnapshotDelete(*crdv1.VolumeSnapshotDataSource, *v1.PersistentVolume) error
	// SnapshotRestore restores (promotes) a volume snapshot into a volume
	SnapshotRestore(*crdv1.VolumeSnapshotData, *v1.PersistentVolumeClaim, string, map[string]string) (*v1.PersistentVolumeSource, map[string]string, error)
	// Describe volume snapshot status.
	// return true if the snapshot is ready
	DescribeSnapshot(snapshotData *crdv1.VolumeSnapshotData) (snapConditions *[]crdv1.VolumeSnapshotCondition, isCompleted bool, err error)
	// FindSnapshot finds a VolumeSnapshot by matching metadata
	FindSnapshot(tags *map[string]string) (*crdv1.VolumeSnapshotDataSource, *[]crdv1.VolumeSnapshotCondition, error)
	// VolumeDelete deletes a PV
	// TODO in the future pass kubernetes client for certain volumes (e.g. rbd) so they can access storage class to retrieve secret
	VolumeDelete(pv *v1.PersistentVolume) error
}
```

### Volume Snapshot Data Source Spec

Each volume must also provide a Snapshot Data Source Spec and add to [VolumeSnapshotDataSource](pkg/apis/crd/v1/types.go), then declare support in [GetSupportedVolumeFromPVC](pkg/apis/crd/v1/types.go) by returning the exact name as returned by the plugin's `GetPluginName()`

### Invocation
The plugins are added to Snapshot controller [cmd pkg](cmd/snapshot-controller/snapshot-controller.go).

