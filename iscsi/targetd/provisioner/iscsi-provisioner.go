package provisioner

import (
	"errors"
	"github.com/Sirupsen/logrus"
	"github.com/powerman/rpc-codec/jsonrpc2"
	//"github.com/kubernetes-incubator/external-storage/iscsi/targetd/provisioner/jsonrpc2"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/pkg/api/v1"
	//"net/rpc"
	//"net/rpc/jsonrpc"
	"sort"
	"strings"
)

var log = logrus.New()

type vol_createArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type vol_destroyArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
}

type export_createArgs struct {
	Pool          string `json:"pool"`
	Vol           string `json:"vol"`
	Initiator_wwn string `json:"initiator_wwn"`
	Lun           int32  `json:"lun"`
}

type export_destroyArgs struct {
	Pool          string `json:"pool"`
	Vol           string `json:"vol"`
	Initiator_wwn string `json:"initiator_wwn"`
}

type iscsiProvisioner struct {
	targetdURL string
}

type export struct {
	Initiator_wwn string `json:"initiator_wwn"`
	Lun           int32  `json:"lun"`
	Vol_name      string `json:"vol_name"`
	Vol_size      int    `json:"vol_size"`
	Vol_uuid      string `json:"vol_uuid"`
	Pool          string `json:"pool"`
}

type exportList []export

type result int

func NewiscsiProvisioner(url string) controller.Provisioner {

	initLog()

	return &iscsiProvisioner{
		targetdURL: url,
	}
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *iscsiProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	log.Debugln("new provision request received for pvc: ", options.PVName)
	vol, lun, pool, err := p.createVolume(options)
	if err != nil {
		log.Warnln(err)
		return nil, err
	}
	log.Debugln("volume created with vol and lun: ", vol, lun)

	annotations := make(map[string]string)
	annotations["volume_name"] = vol
	annotations["pool"] = pool
	annotations["initiators"] = options.Parameters["initiators"]
	//	annotations[annExportBlock] = exportBlock
	//	annotations[annExportID] = strconv.FormatUint(uint64(exportID), 10)
	//	annotations[annProjectBlock] = projectBlock
	//	annotations[annProjectID] = strconv.FormatUint(uint64(projectID), 10)
	//	if supGroup != 0 {
	//		annotations[VolumeGidAnnotationKey] = strconv.FormatUint(supGroup, 10)
	//	}
	//	annotations[annProvisionerID] = string(p.identity)

	pv := &v1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        options.PVName,
			Labels:      map[string]string{},
			Annotations: annotations,
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.PVC.Spec.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)],
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				ISCSI: &v1.ISCSIVolumeSource{
					TargetPortal:   options.Parameters["targetPortal"],
					IQN:            options.Parameters["iqn"],
					ISCSIInterface: options.Parameters["iscsiInterface"],
					Lun:            lun,
					ReadOnly:       false,
					FSType:         "xfs",
				},
			},
		},
	}
	return pv, nil
}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *iscsiProvisioner) Delete(volume *v1.PersistentVolume) error {
	//vol from the annotation
	log.Debugln("volume deletion request received: ", volume.GetName())
	for _, initiator := range strings.Split(volume.Annotations["initiators"], ",") {
		log.Debugln("removing iscsi export: ", volume.Annotations["volume_name"], volume.Annotations["pool"], initiator)
		err := p.export_destroy(volume.Annotations["volume_name"], volume.Annotations["pool"], initiator)
		if err != nil {
			log.Warnln(err)
			return err
		}
		log.Debugln("iscsi export removed: ", volume.Annotations["volume_name"], volume.Annotations["pool"], initiator)
	}
	log.Debugln("removing logical volume : ", volume.Annotations["volume_name"], volume.Annotations["pool"])
	err := p.vol_destroy(volume.Annotations["volume_name"], volume.Annotations["pool"])
	if err != nil {
		log.Warnln(err)
		return err
	}
	log.Debugln("logical volume removed: ", volume.Annotations["volume_name"], volume.Annotations["pool"])
	log.Debugln("volume deletion request completed: ", volume.GetName())
	return nil
}

func initLog() {
	var err error
	log.Level, err = logrus.ParseLevel(viper.GetString("log-level"))
	if err != nil {
		log.Fatalln(err)
	}
}

func (p *iscsiProvisioner) createVolume(options controller.VolumeOptions) (vol string, lun int32, pool string, err error) {
	size := getSize(options)
	vol = p.getVolumeName(options)
	pool = p.getVolumeGroup(options)
	initiators := p.getInitiators(options)
	log.Debugln("calling export_list")
	exportList, err := p.export_list()
	if err != nil {
		log.Warnln(err)
		return "", 0, "", err
	}
	log.Debugln("export_list called")
	lun, err = getFirstAvailableLun(exportList)
	if err != nil {
		log.Warnln(err)
		return "", 0, "", err
	}
	log.Debugln("creating volume name, size, pool: ", vol, size, pool)
	err = p.vol_create(vol, size, pool)
	if err != nil {
		log.Warnln(err)
		return "", 0, "", err
	}
	log.Debugln("created volume name, size, pool: ", vol, size, pool)
	for _, initiator := range initiators {
		log.Debugln("exporting volume name, lun, pool, initiatir: ", vol, lun, pool, initiator)
		err = p.export_create(vol, lun, pool, initiator)
		if err != nil {
			log.Warnln(err)
			return "", 0, "", err
		}
		log.Debugln("exported volume name, lun, pool, initiator ", vol, lun, pool, initiator)
	}
	return vol, lun, pool, nil
}

func getSize(options controller.VolumeOptions) int64 {
	q := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	return q.Value()
}

func (p *iscsiProvisioner) getVolumeName(options controller.VolumeOptions) string {
	return options.PVName
}

func (p *iscsiProvisioner) getVolumeGroup(options controller.VolumeOptions) string {
	if options.Parameters["volumeGroup"] == "" {
		return "vg-targetd"
	}
	return options.Parameters["volumeGroup"]
}

func (p *iscsiProvisioner) getInitiators(options controller.VolumeOptions) []string {
	return strings.Split(options.Parameters["initiators"], ",")
}

func getFirstAvailableLun(exportList exportList) (int32, error) {
	log.Debug("export List: ", exportList)

	sort.Sort(exportList)
	log.Debug("sorted export List: ", exportList)
	//this is sloppy way to remove duplicates
	unique_export := make(map[int32]export)
	for _, export := range exportList {
		unique_export[export.Lun] = export
	}
	log.Debug("unique luns sorted export List: ", unique_export)

	//this is a sloppy way to get the list of luns
	luns := make([]int, len(unique_export), len(unique_export))
	i := 0
	for _, export := range unique_export {
		luns[i] = int(export.Lun)
		i++
	}
	log.Debug("lun list: ", luns)

	if len(luns) >= 255 {
		return -1, errors.New("255 luns allocated no more luns available")
	}

	var sluns sort.IntSlice
	sluns = luns[0:len(luns)]
	sort.Sort(sluns)
	log.Debug("sorted lun list: ", sluns)
	lun := int32(-1)
	for i, clun := range sluns {
		if i < int(clun) {
			lun = int32(i)
			break
		}
	}
	if lun == -1 {
		lun = int32(len(sluns))
	}
	return lun, nil
	//return 0, nil
}

////// json rpc operations ////
func (p *iscsiProvisioner) vol_destroy(vol string, pool string) error {
	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}

	//make arguments object
	args := vol_destroyArgs{
		Pool: pool,
		Name: vol,
	}
	//this will store returned result
	var result result
	//call remote procedure with args
	err = client.Call("vol_destroy", args, &result)
	return err
}

func (p *iscsiProvisioner) export_destroy(vol string, pool string, initiator string) error {

	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}

	//make arguments object
	args := export_destroyArgs{
		Pool:          pool,
		Vol:           vol,
		Initiator_wwn: initiator,
	}
	//this will store returned result
	var result result
	//call remote procedure with args
	err = client.Call("export_destroy", args, &result)
	return err
}

func (p *iscsiProvisioner) vol_create(name string, size int64, pool string) error {

	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}

	//make arguments object
	args := vol_createArgs{
		Pool: pool,
		Name: name,
		Size: size,
	}
	//this will store returned result
	var result result
	//call remote procedure with args
	err = client.Call("vol_create", args, &result)
	return err
}

func (p *iscsiProvisioner) export_create(vol string, lun int32, pool string, initiator string) error {

	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}

	//make arguments object
	args := export_createArgs{
		Pool:          pool,
		Vol:           vol,
		Initiator_wwn: initiator,
		Lun:           lun,
	}
	//this will store returned result
	var result result
	//call remote procedure with args
	err = client.Call("export_create", args, &result)
	return err
}

func (p *iscsiProvisioner) export_list() (exportList, error) {

	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return nil, err
	}

	//this will store returned result
	var result exportList
	//call remote procedure with args
	err = client.Call("export_list", nil, &result)
	return result, err
}

func (slice exportList) Len() int {
	return len(slice)
}

func (slice exportList) Less(i, j int) bool {
	return slice[i].Lun < slice[j].Lun
}

func (slice exportList) Swap(i, j int) {
	slice[i], slice[j] = slice[j], slice[i]
}

func (p *iscsiProvisioner) getConnection() (*jsonrpc2.Client, error) {
	log.Debugln("opening connection to targetd: ", p.targetdURL)

	client := jsonrpc2.NewHTTPClient(p.targetdURL)

	if client == nil {
		log.Warnln("error creating the connection to targetd", p.targetdURL)
		return nil, errors.New("error creating the connection to targetd")
	}
	log.Debugln("targetd client created")
	return client, nil
}

//func (p *iscsiProvisioner) getConnection2() (*rpc.Client, error) {
//	log.Debugln("opening connection to targetd: ", p.targetdURL)
//
//	client, err := jsonrpc.Dial("tcp", p.targetdURL)
//
//	if err != nil {
//		log.Warnln(err)
//		return nil, err
//	}
//	log.Debugln("targetd client created")
//	return client, nil
//}
