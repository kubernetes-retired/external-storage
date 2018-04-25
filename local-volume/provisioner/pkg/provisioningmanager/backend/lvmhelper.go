/*
Copyright 2018 The Kubernetes Authors.

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

package backend

import (
	"bytes"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
)

const defaultRetryCount = 2
const defaultRetryWaitDuration = 5 * time.Second
const invalidInt64 int64 = -1

func getVgSize(vgName string) (size int64, err error) {
	output, err := runCmdWithRetries("vgs", vgName, "--nosuffix", "--units", "b", "--noheading", "-o", "size")
	if err != nil {
		return
	}

	float64Value, err := strconv.ParseFloat(strings.TrimSpace(output), 64)
	if err != nil {
		return
	}
	if float64Value < 0 || float64Value > math.MaxInt64 {
		err = fmt.Errorf("volume size overflow")
		return
	}
	// roundup
	size = int64(float64Value)
	return
}

func createLv(lvName string, sizeInGB int, vgName string) (output string, err error) {
	output, err = runCmdWithRetries("lvcreate", "--name", lvName, "--size", strconv.Itoa(sizeInGB)+"G", vgName)
	return
}

func deleteLv(rootPath string, vgName string, lvName string) (output string, err error) {
	output, err = runCmdWithRetries("lvremove", "-f", getLvPath(rootPath, vgName, lvName))
	return
}

func getLvPath(rootPath, vgName, lvName string) string {
	return rootPath + "/" + vgName + "/" + lvName
}

// TODO: isTransientError should be different with command
func runCmdWithRetries(name string, args ...string) (ret string, err error) {
	var errMsg string
	for i := 0; i <= defaultRetryCount; i++ {
		ret, errMsg, err = runCmd(name, args...)
		if err != nil {
			if !isTransientError() {
				break
			}
			glog.Warningf("the %dth time command[%s, %s] running error: %v, Stderr: %v", i+1, name, args, err, errMsg)
			time.Sleep(defaultRetryWaitDuration)
			continue
		}
		glog.V(2).Infof("the command output: %s", ret)
		break
	}
	return ret, err
}

func runCmd(name string, args ...string) (string, string, error) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func isTransientError() bool {
	return true
}
