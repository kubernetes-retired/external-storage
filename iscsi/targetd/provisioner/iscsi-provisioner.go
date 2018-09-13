/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provisioner

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"github.com/magiconair/properties"
	"github.com/powerman/rpc-codec/jsonrpc2"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var log = logrus.New()

type chapSessionCredentials struct {
	InUser      string `properties:"node.session.auth.username"`
	InPassword  string `properties:"node.session.auth.password"`
	OutUser     string `properties:"node.session.auth.username_in"`
	OutPassword string `properties:"node.session.auth.password_in"`
}

type volCreateArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

//initiator_set_auth(initiator_wwn, in_user, in_pass, out_user, out_pass)
type initiatorSetAuthArgs struct {
	InitiatorWwn string `json:"initiator_wwn"`
	InUser       string `json:"in_user"`
	InPassword   string `json:"in_pass"`
	OutUser      string `json:"out_user"`
	OutPassword  string `json:"out_pass"`
}

type volDestroyArgs struct {
	Pool string `json:"pool"`
	Name string `json:"name"`
}

type exportCreateArgs struct {
	Pool         string `json:"pool"`
	Vol          string `json:"vol"`
	InitiatorWwn string `json:"initiator_wwn"`
	Lun          int32  `json:"lun"`
}

type exportDestroyArgs struct {
	Pool         string `json:"pool"`
	Vol          string `json:"vol"`
	InitiatorWwn string `json:"initiator_wwn"`
}

type iscsiProvisioner struct {
	targetdURL string
}

type export struct {
	InitiatorWwn string `json:"initiator_wwn"`
	Lun          int32  `json:"lun"`
	VolName      string `json:"vol_name"`
	VolSize      int    `json:"vol_size"`
	VolUUID      string `json:"vol_uuid"`
	Pool         string `json:"pool"`
}

type exportList []export

// NewiscsiProvisioner creates new iscsi provisioner
func NewiscsiProvisioner(url string) controller.Provisioner {

	initLog()

	return &iscsiProvisioner{
		targetdURL: url,
	}
}

// getAccessModes returns access modes iscsi volume supported.
func (p *iscsiProvisioner) getAccessModes() []v1.PersistentVolumeAccessMode {
	return []v1.PersistentVolumeAccessMode{
		v1.ReadWriteOnce,
		v1.ReadOnlyMany,
	}
}

// Provision creates a storage asset and returns a PV object representing it.
func (p *iscsiProvisioner) Provision(options controller.VolumeOptions) (*v1.PersistentVolume, error) {
	if !util.AccessModesContainedInAll(p.getAccessModes(), options.PVC.Spec.AccessModes) {
		return nil, fmt.Errorf("invalid AccessModes %v: only AccessModes %v are supported", options.PVC.Spec.AccessModes, p.getAccessModes())
	}
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

	var portals []string
	if len(options.Parameters["portals"]) > 0 {
		portals = strings.Split(options.Parameters["portals"], ",")
	}

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
			// set volumeMode from PVC Spec
			VolumeMode: options.PVC.Spec.VolumeMode,
			PersistentVolumeSource: v1.PersistentVolumeSource{
				ISCSI: &v1.ISCSIPersistentVolumeSource{
					TargetPortal:      options.Parameters["targetPortal"],
					Portals:           portals,
					IQN:               options.Parameters["iqn"],
					ISCSIInterface:    options.Parameters["iscsiInterface"],
					Lun:               lun,
					ReadOnly:          getReadOnly(options.Parameters["readonly"]),
					FSType:            getFsType(options.Parameters["fsType"]),
					DiscoveryCHAPAuth: getBool(options.Parameters["chapAuthDiscovery"]),
					SessionCHAPAuth:   getBool(options.Parameters["chapAuthSession"]),
					SecretRef:         getSecretRef(getBool(options.Parameters["chapAuthDiscovery"]), getBool(options.Parameters["chapAuthSession"]), &v1.SecretReference{Name: viper.GetString("provisioner-name") + "-chap-secret"}),
				},
			},
		},
	}
	return pv, nil
}

func getReadOnly(readonly string) bool {
	isReadOnly, err := strconv.ParseBool(readonly)
	if err != nil {
		return false
	}
	return isReadOnly
}

func getFsType(fsType string) string {
	if fsType == "" {
		return viper.GetString("default-fs")
	}
	return fsType
}

func getSecretRef(discovery bool, session bool, ref *v1.SecretReference) *v1.SecretReference {
	if discovery || session {
		return ref
	}
	return nil
}

func getBool(value string) bool {
	res, err := strconv.ParseBool(value)
	if err != nil {
		return false
	}
	return res

}

// Delete removes the storage asset that was created by Provision represented
// by the given PV.
func (p *iscsiProvisioner) Delete(volume *v1.PersistentVolume) error {
	//vol from the annotation
	log.Debugln("volume deletion request received: ", volume.GetName())
	for _, initiator := range strings.Split(volume.Annotations["initiators"], ",") {
		log.Debugln("removing iscsi export: ", volume.Annotations["volume_name"], volume.Annotations["pool"], initiator)
		err := p.exportDestroy(volume.Annotations["volume_name"], volume.Annotations["pool"], initiator)
		if err != nil {
			log.Warnln(err)
			return err
		}
		log.Debugln("iscsi export removed: ", volume.Annotations["volume_name"], volume.Annotations["pool"], initiator)
	}
	log.Debugln("removing logical volume : ", volume.Annotations["volume_name"], volume.Annotations["pool"])
	err := p.volDestroy(volume.Annotations["volume_name"], volume.Annotations["pool"])
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
	chapCredentials := &chapSessionCredentials{}
	//read chap session authentication credentials
	if getBool(options.Parameters["chapAuthSession"]) {
		prop, err2 := properties.LoadFile(viper.GetString("session-chap-credential-file-path"), properties.UTF8)
		if err2 != nil {
			log.Warnln(err2)
			return "", 0, "", err2
		}
		err2 = prop.Decode(chapCredentials)
		if err2 != nil {
			log.Warnln(err2)
			return "", 0, "", err2
		}
	}

	log.Debugln("calling export_list")
	exportList1, err := p.exportList()
	if err != nil {
		log.Warnln(err)
		return "", 0, "", err
	}
	log.Debugln("export_list called")
	lun, err = getFirstAvailableLun(exportList1)
	if err != nil {
		log.Warnln(err)
		return "", 0, "", err
	}
	log.Debugln("creating volume name, size, pool: ", vol, size, pool)
	err = p.volCreate(vol, size, pool)
	if err != nil {
		log.Warnln(err)
		return "", 0, "", err
	}
	log.Debugln("created volume name, size, pool: ", vol, size, pool)
	for _, initiator := range initiators {
		log.Debugln("exporting volume name, lun, pool, initiator: ", vol, lun, pool, initiator)
		err = p.exportCreate(vol, lun, pool, initiator)
		if err != nil {
			log.Warnln(err)
			return "", 0, "", err
		}
		log.Debugln("exported volume name, lun, pool, initiator ", vol, lun, pool, initiator)
		if getBool(options.Parameters["chapAuthSession"]) {
			log.Debugln("setting up chap session auth for initiator, initiator, in_user, out_user: ", initiator, chapCredentials.InUser, chapCredentials.OutUser)
			err = p.setInitiatorAuth(initiator, chapCredentials.InUser, chapCredentials.InPassword, chapCredentials.OutUser, chapCredentials.OutPassword)
			if err != nil {
				log.Warnln(err)
				return "", 0, "", err
			}
			log.Debugln("set up chap session auth for initiator, initiator, in_user, out_user: ", initiator, chapCredentials.InUser, chapCredentials.OutUser)
		}
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

// getFirstAvailableLun gets first available Lun.
func getFirstAvailableLun(exportList exportList) (int32, error) {
	sort.Sort(exportList)
	log.Debug("sorted export List: ", exportList)
	//this is sloppy way to remove duplicates
	uniqueExport := make(map[int32]export)
	for _, export := range exportList {
		uniqueExport[export.Lun] = export
	}
	log.Debug("unique luns sorted export List: ", uniqueExport)

	//this is a sloppy way to get the list of luns
	luns := make([]int, len(uniqueExport), len(uniqueExport))
	i := 0
	for _, export := range uniqueExport {
		luns[i] = int(export.Lun)
		i++
	}
	log.Debug("lun list: ", luns)

	if len(luns) >= 255 {
		return -1, errors.New("255 luns allocated no more luns available")
	}

	var sluns sort.IntSlice
	sluns = luns[0:]
	sort.Sort(sluns)
	log.Debug("sorted lun list: ", sluns)

	lun := int32(len(sluns))
	for i, clun := range sluns {
		if i < int(clun) {
			lun = int32(i)
			break
		}
	}
	return lun, nil
}

// volDestroy removes calls vol_destroy targetd API to remove volume.
func (p *iscsiProvisioner) volDestroy(vol string, pool string) error {
	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}
	args := volDestroyArgs{
		Pool: pool,
		Name: vol,
	}
	err = client.Call("vol_destroy", args, nil)
	return err
}

// exportDestroy calls export_destroy targetd API to remove export of volume.
func (p *iscsiProvisioner) exportDestroy(vol string, pool string, initiator string) error {
	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}
	args := exportDestroyArgs{
		Pool:         pool,
		Vol:          vol,
		InitiatorWwn: initiator,
	}
	err = client.Call("export_destroy", args, nil)
	return err
}

// volCreate calls vol_create targetd API to create a volume.
func (p *iscsiProvisioner) volCreate(name string, size int64, pool string) error {
	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}
	args := volCreateArgs{
		Pool: pool,
		Name: name,
		Size: size,
	}
	err = client.Call("vol_create", args, nil)
	return err
}

// exportCreate calls export_create targetd API to create an export of volume.
func (p *iscsiProvisioner) exportCreate(vol string, lun int32, pool string, initiator string) error {
	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}
	args := exportCreateArgs{
		Pool:         pool,
		Vol:          vol,
		InitiatorWwn: initiator,
		Lun:          lun,
	}
	err = client.Call("export_create", args, nil)
	return err
}

// exportList lists calls export_list targetd API to get export objects.
func (p *iscsiProvisioner) exportList() (exportList, error) {
	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return nil, err
	}
	var result1 exportList
	err = client.Call("export_list", nil, &result1)
	return result1, err
}

//initiator_set_auth(initiator_wwn, in_user, in_pass, out_user, out_pass)

func (p *iscsiProvisioner) setInitiatorAuth(initiator string, inUser string, inPassword string, outUser string, outPassword string) error {

	client, err := p.getConnection()
	defer client.Close()
	if err != nil {
		log.Warnln(err)
		return err
	}

	//make arguments object
	args := initiatorSetAuthArgs{
		InitiatorWwn: initiator,
		InUser:       inUser,
		InPassword:   inPassword,
		OutUser:      outUser,
		OutPassword:  outPassword,
	}
	//call remote procedure with args
	err = client.Call("initiator_set_auth", args, nil)
	return err
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

func (p *iscsiProvisioner) SupportsBlock() bool {
	return true
}
