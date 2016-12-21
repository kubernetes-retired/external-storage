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
	"k8s.io/client-go/pkg/api/v1"
)

type exporter interface {
	AddExportBlock(string) (string, uint16, error)
	RemoveExportBlock(string, uint16) error
	Export(string) error
	Unexport(*v1.PersistentVolume) error
}

type exportBlockCreator interface {
	CreateExportBlock(string, string) string
}

type genericExporter struct {
	ebc    exportBlockCreator
	config string

	// Map to track used exportIds. Each ganesha export needs a unique fsid and
	// Export_Id, each kernel a unique fsid. Assign each export an exportId and
	// use it as both fsid and Export_Id.
	exportIds map[uint16]bool

	mapMutex  *sync.Mutex
	fileMutex *sync.Mutex
}

func newGenericExporter(ebc exportBlockCreator, config string, re *regexp.Regexp) *genericExporter {
	if _, err := os.Stat(config); os.IsNotExist(err) {
		glog.Fatalf("config %s does not exist!", config)
	}

	exportIds, err := getExistingIds(config, re)
	if err != nil {
		glog.Errorf("error while populating exportIds map, there may be errors exporting later if exportIds are reused: %v", err)
	}
	return &genericExporter{
		ebc:       ebc,
		config:    config,
		exportIds: exportIds,
		mapMutex:  &sync.Mutex{},
		fileMutex: &sync.Mutex{},
	}
}

func (e *genericExporter) AddExportBlock(path string) (string, uint16, error) {
	exportId := generateId(e.mapMutex, e.exportIds)
	exportIdStr := strconv.FormatUint(uint64(exportId), 10)

	block := e.ebc.CreateExportBlock(exportIdStr, path)

	// Add the export block to the config file
	if err := addToFile(e.fileMutex, e.config, block); err != nil {
		deleteId(e.mapMutex, e.exportIds, exportId)
		return "", 0, fmt.Errorf("error adding export block %s to config %s: %v", block, e.config, err)
	}
	return block, exportId, nil
}

func (e *genericExporter) RemoveExportBlock(block string, exportId uint16) error {
	deleteId(e.mapMutex, e.exportIds, exportId)
	return removeFromFile(e.fileMutex, e.config, block)
}

type ganeshaExporter struct {
	genericExporter
}

var _ exporter = &ganeshaExporter{}

func newGaneshaExporter(ganeshaConfig string, rootSquash bool) exporter {
	return &ganeshaExporter{
		genericExporter: *newGenericExporter(&ganeshaExportBlockCreator{rootSquash}, ganeshaConfig, regexp.MustCompile("Export_Id = ([0-9]+);")),
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
	ann, ok := volume.Annotations[annExportId]
	if !ok {
		return fmt.Errorf("PV doesn't have an annotation %s, can't remove the export from the server", annExportId)
	}
	exportId, _ := strconv.ParseUint(ann, 10, 16)

	// Call RemoveExport using dbus
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("error getting dbus session bus: %v", err)
	}
	obj := conn.Object("org.ganesha.nfsd", "/org/ganesha/nfsd/ExportMgr")
	call := obj.Call("org.ganesha.nfsd.exportmgr.RemoveExport", 0, uint16(exportId))
	if call.Err != nil {
		return fmt.Errorf("error calling org.ganesha.nfsd.exportmgr.RemoveExport: %v", call.Err)
	}

	return nil
}

type ganeshaExportBlockCreator struct {
	// Whether to export with squash = root_id_squash, not no_root_squash
	rootSquash bool
}

var _ exportBlockCreator = &ganeshaExportBlockCreator{}

// CreateBlock creates the text block to add to the ganesha config file.
func (e *ganeshaExportBlockCreator) CreateExportBlock(exportId, path string) string {
	squash := "no_root_squash"
	if e.rootSquash {
		squash = "root_id_squash"
	}
	return "\nEXPORT\n{\n" +
		"\tExport_Id = " + exportId + ";\n" +
		"\tPath = " + path + ";\n" +
		"\tPseudo = " + path + ";\n" +
		"\tAccess_Type = RW;\n" +
		"\tSquash = " + squash + ";\n" +
		"\tSecType = sys;\n" +
		"\tFilesystem_id = " + exportId + "." + exportId + ";\n" +
		"\tFSAL {\n\t\tName = VFS;\n\t}\n}\n"
}

type kernelExporter struct {
	genericExporter
}

var _ exporter = &kernelExporter{}

func newKernelExporter(rootSquash bool) exporter {
	return &kernelExporter{
		genericExporter: *newGenericExporter(&kernelExportBlockCreator{rootSquash}, "/etc/exports", regexp.MustCompile("fsid=([0-9]+)")),
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

type kernelExportBlockCreator struct {
	// Whether to export with option root_squash, not no_root_squash
	rootSquash bool
}

var _ exportBlockCreator = &kernelExportBlockCreator{}

// CreateBlock creates the text block to add to the /etc/exports file.
func (e *kernelExportBlockCreator) CreateExportBlock(exportId, path string) string {
	squash := "no_root_squash"
	if e.rootSquash {
		squash = "root_squash"
	}
	return "\n" + path + " *(rw,insecure," + squash + ",fsid=" + exportId + ")\n"
}
