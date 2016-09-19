package volume

import (
	"k8s.io/client-go/1.4/pkg/api/resource"
	"k8s.io/client-go/1.4/pkg/api/unversioned"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

// VolumeOptions contains option information about a volume
// https://github.com/kubernetes/kubernetes/blob/master/pkg/volume/plugins.go
type VolumeOptions struct {
	// Capacity is the size of a volume.
	Capacity resource.Quantity
	// AccessModes of a volume
	AccessModes []v1.PersistentVolumeAccessMode
	// Reclamation policy for a persistent volume
	PersistentVolumeReclaimPolicy v1.PersistentVolumeReclaimPolicy
	// PV.Name of the appropriate PersistentVolume. Used to generate cloud
	// volume name.
	PVName string
	// Volume provisioning parameters from StorageClass
	Parameters map[string]string
	// Volume selector from PersistentVolumeClaim
	Selector *unversioned.LabelSelector
}
