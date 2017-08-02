/*
Copyright 2017 The Kubernetes Authors.

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

package util

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"

	"github.com/golang/glog"

	"k8s.io/kubernetes/pkg/volume/util"
	"unsafe"
)

// VolumeUtil is an interface for local filesystem operations
type VolumeUtil interface {
	// IsDir checks if the given path is a directory
	IsDir(fullPath string) (bool, error)

	// IsBlock checks if the given path is a directory
	IsBlock(fullPath string) (bool, error)

	// ReadDir returns a list of files under the specified directory
	ReadDir(fullPath string) ([]string, error)

	// Delete all the contents under the given path, but not the path itself
	DeleteContents(fullPath string) error

	// Get capacity for fs on full path
	GetFsCapacityByte(fullPath string) (int64, error)

	// Get capacity of the block device
	GetBlockCapacityByte(fullPath string) (int64, error)
}

var _ VolumeUtil = &volumeUtil{}

type volumeUtil struct{}

// NewVolumeUtil returns a VolumeUtil object for performing local filesystem operations
func NewVolumeUtil() VolumeUtil {
	return &volumeUtil{}
}

// IsDir checks if the given path is a directory
func (u *volumeUtil) IsDir(fullPath string) (bool, error) {
	dir, err := os.Open(fullPath)
	if err != nil {
		return false, err
	}
	defer dir.Close()

	stat, err := dir.Stat()
	if err != nil {
		return false, err
	}

	return stat.IsDir(), nil
}

// IsBlock checks if the given path is a block device
func (u *volumeUtil) IsBlock(fullPath string) (bool, error) {
	var st unix.Stat_t
	err := unix.Stat(fullPath, &st)
	if err != nil {
		return false, err
	}

	return (st.Mode & unix.S_IFMT) == unix.S_IFBLK, nil
}

// ReadDir returns a list all the files under the given directory
func (u *volumeUtil) ReadDir(fullPath string) ([]string, error) {
	dir, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	files, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	return files, nil
}

// DeleteContents deletes all the contents under the given directory
func (u *volumeUtil) DeleteContents(fullPath string) error {
	dir, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer dir.Close()

	files, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}

	for _, file := range files {
		err = os.RemoveAll(filepath.Join(fullPath, file))
		if err != nil {
			// TODO: accumulate errors
			return err
		}
	}
	return nil
}

// GetFsCapacityByte returns capacity in bytes about a mounted filesystem.
// fullPath is the pathname of any file within the mounted filesystem. Capacity
// returned here is total capacity.
func (u *volumeUtil) GetFsCapacityByte(fullPath string) (int64, error) {
	_, capacity, _, _, _, _, err := util.FsInfo(fullPath)
	return capacity, err
}

// GetBlockCapacityByte returns  capacity in bytes of a block device.
// fullPath is the pathname of block device.
func (u *volumeUtil) GetBlockCapacityByte(fullPath string) (int64, error) {
	file, err := os.OpenFile(fullPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	var size int64
	// Get size of block device into 64 bit int.
	// Ref: http://www.microhowto.info/howto/get_the_size_of_a_linux_block_special_device_in_c.html
	if _, _, err := unix.Syscall(unix.SYS_IOCTL, file.Fd(), unix.BLKGETSIZE64, uintptr(unsafe.Pointer(&size))); err != 0 {
		return 0, err
	}

	return size, err
}

var _ VolumeUtil = &FakeVolumeUtil{}

// FakeVolumeUtil is a stub interface for unit testing
type FakeVolumeUtil struct {
	// List of files underneath the given path
	directoryFiles map[string][]*FakeDirEntry
	// True if DeleteContents should fail
	deleteShouldFail bool
}

const (
	// FakeEntryFile is mock dir entry of type file.
	FakeEntryFile = "file"
	// FakeEntryBlock is mock dir entry of type block.
	FakeEntryBlock = "block"
	// FakeEntryUnknown is mock dir entry of type unknown.
	FakeEntryUnknown = "unknown"
)

// FakeDirEntry contains a representation of a file under a directory
type FakeDirEntry struct {
	Name       string
	VolumeType string
	// Expected hash value of the PV name
	Hash     uint32
	Capacity int64
}

// NewFakeVolumeUtil returns a VolumeUtil object for use in unit testing
func NewFakeVolumeUtil(deleteShouldFail bool) *FakeVolumeUtil {
	return &FakeVolumeUtil{
		directoryFiles:   map[string][]*FakeDirEntry{},
		deleteShouldFail: deleteShouldFail,
	}
}

// IsDir checks if the given path is a directory
func (u *FakeVolumeUtil) IsDir(fullPath string) (bool, error) {
	dir, file := filepath.Split(fullPath)
	dir = filepath.Clean(dir)
	files, found := u.directoryFiles[dir]
	if !found {
		return false, fmt.Errorf("Directory %q not found", dir)
	}

	for _, f := range files {
		if file == f.Name {
			if f.VolumeType != FakeEntryFile {
				// Accurately simulate how a check on a non file returns error with actual OS call.
				return false, fmt.Errorf("%q not a file or directory", fullPath)
			}
			return true, nil
		}
	}
	return false, fmt.Errorf("Directory entry %q not found", fullPath)
}

// IsBlock checks if the given path is a block device
func (u *FakeVolumeUtil) IsBlock(fullPath string) (bool, error) {
	dir, file := filepath.Split(fullPath)
	dir = filepath.Clean(dir)
	files, found := u.directoryFiles[dir]
	if !found {
		return false, fmt.Errorf("Directory %q not found", dir)
	}

	for _, f := range files {
		if file == f.Name {
			return f.VolumeType == FakeEntryBlock, nil
		}
	}
	return false, fmt.Errorf("Directory entry %q not found", fullPath)
}

// ReadDir returns the list of all files under the given directory
func (u *FakeVolumeUtil) ReadDir(fullPath string) ([]string, error) {
	fileNames := []string{}
	files, found := u.directoryFiles[fullPath]
	if !found {
		return nil, fmt.Errorf("Directory %q not found", fullPath)
	}
	for _, file := range files {
		fileNames = append(fileNames, file.Name)
	}
	return fileNames, nil
}

// DeleteContents removes all the contents under the given directory
func (u *FakeVolumeUtil) DeleteContents(fullPath string) error {
	if u.deleteShouldFail {
		return fmt.Errorf("Fake delete contents failed")
	}
	return nil
}

// GetFsCapacityByte returns capacity in byte about a mounted filesystem.
func (u *FakeVolumeUtil) GetFsCapacityByte(fullPath string) (int64, error) {
	return u.getDirEntryCapacity(fullPath, FakeEntryFile)
}

// GetBlockCapacityByte returns the space in the specified block device.
func (u *FakeVolumeUtil) GetBlockCapacityByte(fullPath string) (int64, error) {
	return u.getDirEntryCapacity(fullPath, FakeEntryBlock)
}

func (u *FakeVolumeUtil) getDirEntryCapacity(fullPath string, entryType string) (int64, error) {
	dir, file := filepath.Split(fullPath)
	dir = filepath.Clean(dir)
	files, found := u.directoryFiles[dir]
	if !found {
		return 0, fmt.Errorf("Directory %q not found", dir)
	}

	for _, f := range files {
		if file == f.Name {
			if f.VolumeType != entryType {
				return 0, fmt.Errorf("Directory entry %q is not a %q", f, entryType)
			}
			return f.Capacity, nil
		}
	}
	return 0, fmt.Errorf("Directory entry %q not found", fullPath)
}

// AddNewDirEntries adds the given files to the current directory listing
// This is only for testing
func (u *FakeVolumeUtil) AddNewDirEntries(mountDir string, dirFiles map[string][]*FakeDirEntry) {
	for dir, files := range dirFiles {
		mountedPath := filepath.Join(mountDir, dir)
		curFiles := u.directoryFiles[mountedPath]
		if curFiles == nil {
			curFiles = []*FakeDirEntry{}
		}
		glog.Infof("Adding to directory %q: files %v\n", dir, files)
		u.directoryFiles[mountedPath] = append(curFiles, files...)
	}
}
