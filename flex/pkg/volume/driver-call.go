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

package volume

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Option keys
	provisionCmd = "provision"
	deleteCmd    = "delete"
)

// ErrorTimeout defines the time error
var ErrorTimeout = fmt.Errorf("Timeout")

const (
	// StatusSuccess represents the successful completion of command.
	StatusSuccess = "Success"
	// StatusNotSupported represents that the command is not supported.
	StatusNotSupported = "Not supported"
)

// DriverCall implements the basic contract between FlexVolume and its driver.
// The caller is responsible for providing the required args.
type DriverCall struct {
	Execpath string
	Command  string
	Timeout  time.Duration
	plugin   *flexProvisioner
	args     []string
}

// NewDriverCall initialize the DriverCall
func (plugin *flexProvisioner) NewDriverCall(execPath, command string) *DriverCall {
	return plugin.NewDriverCallWithTimeout(execPath, command, 0)
}

//NewDriverCallWithTimeout return the DriverCall with timeout
func (plugin *flexProvisioner) NewDriverCallWithTimeout(execPath, command string, timeout time.Duration) *DriverCall {
	return &DriverCall{
		Execpath: execPath,
		Command:  command,
		Timeout:  timeout,
		plugin:   plugin,
		args:     []string{command},
	}
}

//Append add arg to DriverCall
func (dc *DriverCall) Append(arg string) {
	dc.args = append(dc.args, arg)
}

//AppendSpec add all option parameters to DriverCall
func (dc *DriverCall) AppendSpec(options interface{}) error {
	jsonBytes, err := json.Marshal(options)
	if err != nil {
		return fmt.Errorf("Failed to marshal spec, error: %s", err.Error())
	}

	dc.Append(string(jsonBytes))
	return nil
}

//Run the command with option parameters
func (dc *DriverCall) Run() (*DriverStatus, error) {
	cmd := dc.plugin.runner.Command(dc.Execpath, dc.args...)

	timeout := false
	if dc.Timeout > 0 {
		timer := time.AfterFunc(dc.Timeout, func() {
			timeout = true
			cmd.Stop()
		})
		defer timer.Stop()
	}

	output, execErr := cmd.CombinedOutput()
	if execErr != nil {
		if timeout {
			return nil, ErrorTimeout
		}
		_, err := handleCmdResponse(dc.Command, output)
		if err == nil {
			glog.Errorf("FlexVolume: driver bug: %s: exec error (%s) but no error in response.", dc.Execpath, execErr)
			return nil, execErr
		}

		glog.Warningf("FlexVolume: driver call failed: executable: %s, args: %s, error: %s, output: %q", dc.Execpath, dc.args, execErr.Error(), output)
		return nil, err
	}

	status, err := handleCmdResponse(dc.Command, output)
	if err != nil {
		return nil, err
	}

	return status, nil
}

// DriverStatus represents the return value of the driver callout.
type DriverStatus struct {
	// Status of the callout. One of "Success", "Failure" or "Not supported".
	Status string `json:"status"`
	// Reason for success/failure.
	Message string `json:"message,omitempty"`
	// volume object for kubernetes
	Volume v1.PersistentVolume `json:"volume,omitempty"`
}

// handleCmdResponse processes the command output and returns the appropriate
// error code or message.
func handleCmdResponse(cmd string, output []byte) (*DriverStatus, error) {
	status := &DriverStatus{
		Volume: v1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{},
				Labels:      map[string]string{},
			}}}
	if err := json.Unmarshal(output, status); err != nil {
		glog.Errorf("Failed to unmarshal output for command: %s, output: %q, error: %s", cmd, string(output), err.Error())
		return nil, err
	} else if status.Status == StatusNotSupported {
		glog.V(5).Infof("%s command is not supported by the driver", cmd)
		return nil, errors.New(status.Status)
	} else if status.Status != StatusSuccess {
		errMsg := fmt.Sprintf("%s command failed, status: %s, reason: %s", cmd, status.Status, status.Message)
		glog.Errorf(errMsg)
		return nil, fmt.Errorf("%s", errMsg)
	}

	return status, nil
}
