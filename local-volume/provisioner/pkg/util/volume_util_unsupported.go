// +build !linux

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
)

// GetBlockCapacityByte is defined here for darwin and other platforms
// so that make test suceeds on them.
func (u *volumeUtil) GetBlockCapacityByte(fullPath string) (int64, error) {
	return 0, fmt.Errorf("GetBlockCapacityByte is unsupported in this build")
}

// IsBlock for unsupported platform returns error.
func (u *volumeUtil) IsBlock(fullPath string) (bool, error) {
	return false, fmt.Errorf("IsBlock is unsupported in this build")
}
