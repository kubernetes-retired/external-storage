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
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/golang/glog"
)

// VolumeUtil is an interface for local filesystem operations
type VolumeUtil interface {
	// IsDir checks if the given path is a directory
	IsDir(fullPath string) (bool, error)

	// ReadDir returns a list of files under the specified directory
	ReadDir(fullPath string) ([]string, error)

	// Delete all the contents under the given path, but not the path itself
	DeleteContents(fullPath string) error

	// Get available capacity for fs on full path
	GetFsAvailableByte(fullPath string) (uint64, error)
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

// GetFsAvailableByte returns available capacity in byte about a mounted filesystem.
// fullPath is the pathname of any file within the mounted filesystem. Capacity
// returned here is total capacity available to non-root users, and does not include
// fs reserved space.
func (u *volumeUtil) GetFsAvailableByte(fullPath string) (uint64, error) {
	var s unix.Statfs_t
	if err := unix.Statfs(fullPath, &s); err != nil {
		return 0, err
	}
	return uint64(s.Frsize) * (s.Blocks - s.Bfree + s.Bavail), nil
}

var _ VolumeUtil = &FakeVolumeUtil{}

// FakeVolumeUtil is a stub interface for unit testing
type FakeVolumeUtil struct {
	// List of files underneath the given path
	directoryFiles map[string][]*FakeFile
	// True if DeleteContents should fail
	deleteShouldFail bool
}

// FakeFile contains a representation of a file under a directory
type FakeFile struct {
	Name     string
	IsNotDir bool
	// Expected hash value of the PV name
	Hash     uint32
	Capacity uint64
}

// NewFakeVolumeUtil returns a VolumeUtil object for use in unit testing
func NewFakeVolumeUtil(deleteShouldFail bool) *FakeVolumeUtil {
	return &FakeVolumeUtil{
		directoryFiles:   map[string][]*FakeFile{},
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
			return !f.IsNotDir, nil
		}
	}
	return false, fmt.Errorf("File %q not found", fullPath)
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

// GetFsAvailableByte returns available capacity in byte about a mounted filesystem.
func (u *FakeVolumeUtil) GetFsAvailableByte(fullPath string) (uint64, error) {
	dir, file := filepath.Split(fullPath)
	dir = filepath.Clean(dir)
	files, found := u.directoryFiles[dir]
	if !found {
		return 0, fmt.Errorf("Directory %q not found", dir)
	}

	for _, f := range files {
		if file == f.Name {
			return f.Capacity, nil
		}
	}
	return 0, fmt.Errorf("File %q not found", fullPath)
}

// AddNewFiles adds the given files to the current directory listing
// This is only for testing
func (u *FakeVolumeUtil) AddNewFiles(mountDir string, dirFiles map[string][]*FakeFile) {
	for dir, files := range dirFiles {
		mountedPath := filepath.Join(mountDir, dir)
		curFiles := u.directoryFiles[mountedPath]
		if curFiles == nil {
			curFiles = []*FakeFile{}
		}
		glog.Infof("Adding to directory %q: files %v\n", dir, files)
		u.directoryFiles[mountedPath] = append(curFiles, files...)
	}
}
