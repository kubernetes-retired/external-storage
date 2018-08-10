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

package volume

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"

	"github.com/golang/glog"
	"github.com/guelfey/go.dbus"
	"k8s.io/api/core/v1"
)

type exporter interface {
	CanExport(int) bool
	AddExportBlock(string, bool, string) (string, uint16, error)
	RemoveExportBlock(string, uint16) error
	Export(string) error
	Unexport(*v1.PersistentVolume) error
}

type exportBlockCreator interface {
	CreateExportBlock(string, string, bool, string) string
}

type exportMap struct {
	// Map to track used exportIDs. Each ganesha export needs a unique fsid and
	// Export_Id, each kernel a unique fsid. Assign each export an exportID and
	// use it as both fsid and Export_Id.
	exportIDs map[uint16]bool
}

func (e *exportMap) CanExport(limit int) bool {
	if limit < 0 {
		return true
	}

	totalExports := len(e.exportIDs)
	return totalExports < limit
}

type genericExporter struct {
	*exportMap

	ebc    exportBlockCreator
	config string

	mapMutex  *sync.Mutex
	fileMutex *sync.Mutex
}

func newGenericExporter(ebc exportBlockCreator, config string, re *regexp.Regexp) *genericExporter {
	if _, err := os.Stat(config); os.IsNotExist(err) {
		glog.Fatalf("config %s does not exist!", config)
	}

	exportIDs, err := getExistingIDs(config, re)
	if err != nil {
		glog.Errorf("error while populating exportIDs map, there may be errors exporting later if exportIDs are reused: %v", err)
	}
	return &genericExporter{
		exportMap: &exportMap{
			exportIDs: exportIDs,
		},
		ebc:       ebc,
		config:    config,
		mapMutex:  &sync.Mutex{},
		fileMutex: &sync.Mutex{},
	}
}

func (e *genericExporter) AddExportBlock(path string, rootSquash bool, exportSubnet string) (string, uint16, error) {
	exportID := generateID(e.mapMutex, e.exportIDs)
	exportIDStr := strconv.FormatUint(uint64(exportID), 10)

	block := e.ebc.CreateExportBlock(exportIDStr, path, rootSquash, exportSubnet)

	// Add the export block to the config file
	if err := addToFile(e.fileMutex, e.config, block); err != nil {
		deleteID(e.mapMutex, e.exportIDs, exportID)
		return "", 0, fmt.Errorf("error adding export block %s to config %s: %v", block, e.config, err)
	}
	return block, exportID, nil
}

func (e *genericExporter) RemoveExportBlock(block string, exportID uint16) error {
	deleteID(e.mapMutex, e.exportIDs, exportID)
	return removeFromFile(e.fileMutex, e.config, block)
}

type ganeshaExporter struct {
	genericExporter
}

var _ exporter = &ganeshaExporter{}

func newGaneshaExporter(ganeshaConfig string) exporter {
	return &ganeshaExporter{
		genericExporter: *newGenericExporter(&ganeshaExportBlockCreator{}, ganeshaConfig, regexp.MustCompile("Export_Id = ([0-9]+);")),
	}
}

// Export exports the given directory using NFS Ganesha, assuming it is running
// and can be connected to using D-Bus.
func (e *ganeshaExporter) Export(path string) error {
	// Call AddExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.AddExport", 0, e.config, fmt.Sprintf("export(path = %s)", path))
	if call.Err != nil {
		return fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.AddExport: %v", call.Err)
	}

	return nil
}

func (e *ganeshaExporter) Unexport(volume *v1.PersistentVolume) error {
	ann, ok := volume.Annotations[annExportID]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the server", annExportID)
	}
	exportID, _ := strconv.ParseUint(ann, 10, 16)

	// Call RemoveExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.RemoveExport", 0, uint16(exportID))
	if call.Err != nil {
		return fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.RemoveExport: %v", call.Err)
	}

	return nil
}

type ganeshaExportBlockCreator struct{}

var _ exportBlockCreator = &ganeshaExportBlockCreator{}

// CreateBlock creates the text block to add to the ganesha config file.
func (e *ganeshaExportBlockCreator) CreateExportBlock(exportID, path string, rootSquash bool, exportSubnet string) string {
	squash := "no_root_squash"
	if rootSquash {
		squash = "root_id_squash"
	}
	return "\nEXPORT\n{\n" +
		"\tExport_Id = " + exportID + ";\n" +
		"\tPath = " + path + ";\n" +
		"\tPseudo = " + path + ";\n" +
		"\tAccess_Type = RW;\n" +
		"\tSquash = " + squash + ";\n" +
		"\tSecType = sys;\n" +
		"\tFilesystem_id = " + exportID + "." + exportID + ";\n" +
		"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n"
}

type kernelExporter struct {
	genericExporter
}

var _ exporter = &kernelExporter{}

func newKernelExporter() exporter {
	return &kernelExporter{
		genericExporter: *newGenericExporter(&kernelExportBlockCreator{}, "/etc/exports", regexp.MustCompile("fsid=([0-9]+)")),
	}
}

// Export exports all directories listed in /etc/exports
func (e *kernelExporter) Export(_ string) error {
	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return nil
}

func (e *kernelExporter) Unexport(volume *v1.PersistentVolume) error {
	// Execute exportfs
	cmd := exec.Command("exportfs", "-r")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("exportfs -r failed with error: %v, output: %s", err, out)
	}

	return nil
}

type kernelExportBlockCreator struct{}

var _ exportBlockCreator = &kernelExportBlockCreator{}

// CreateBlock creates the text block to add to the /etc/exports file.
func (e *kernelExportBlockCreator) CreateExportBlock(exportID, path string, rootSquash bool, exportSubnet string) string {
	squash := "no_root_squash"
	if rootSquash {
		squash = "root_squash"
	}
	return "\n" + path + " " + exportSubnet + "(rw,insecure," + squash + ",fsid=" + exportID + ")\n"
}
