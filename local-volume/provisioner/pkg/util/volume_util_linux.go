// +build linux

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
	"golang.org/x/sys/unix"
	"os"
	"unsafe"
)

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
