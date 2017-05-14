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
	"os"
	"path/filepath"
)

type VolumeUtil interface {
	// ReadDir returns a list of files under the specified directory
	ReadDir(fullPath string) ([]string, error)

	// Delete all the contents under the given path, but not the path itself
	DeleteContents(fullPath string) error
}

var _ VolumeUtil = &volumeUtil{}

type volumeUtil struct{}

func NewVolumeUtil() VolumeUtil {
	return &volumeUtil{}
}

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

var _ VolumeUtil = &FakeVolumeUtil{}

type FakeVolumeUtil struct {
	// List of files underneath the given path
	directoryFiles map[string][]string
}

func NewFakeVolumeUtil() *FakeVolumeUtil {
	return &FakeVolumeUtil{
		directoryFiles: map[string][]string{},
	}
}

func (u *FakeVolumeUtil) ReadDir(fullPath string) ([]string, error) {
	return u.directoryFiles[fullPath], nil
}

func (u *FakeVolumeUtil) DeleteContents(fullPath string) error {
	return nil
}

// AddNewFiles adds the given files to the current directory listing
// This is only for testing
func (u *FakeVolumeUtil) AddNewFiles(mountDir string, dirFiles map[string][]string) {
	for dir, files := range dirFiles {
		mountedPath := filepath.Join(mountDir, dir)
		curFiles := u.directoryFiles[mountedPath]
		if curFiles == nil {
			curFiles = []string{}
		}
		u.directoryFiles[mountedPath] = append(curFiles, files...)
	}
}
