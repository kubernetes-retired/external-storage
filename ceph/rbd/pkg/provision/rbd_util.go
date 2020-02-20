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

package provision

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kubernetes-sigs/sig-storage-lib-external-provisioner/controller"
	"github.com/kubernetes-sigs/sig-storage-lib-external-provisioner/util"
	"k8s.io/api/core/v1"
	"k8s.io/klog"
)

const (
	imageWatcherStr = "watcher="
)

// RBDUtil is the utility structure to interact with the RBD.
type RBDUtil struct {
	// Command execution timeout
	timeout int
}

// See https://github.com/kubernetes/kubernetes/pull/57512.
func (u *RBDUtil) kernelRBDMonitorsOpt(mons []string) string {
	return strings.Join(mons, ",")
}

// CreateImage creates a new ceph image with provision and volume options.
func (u *RBDUtil) CreateImage(image string, pOpts *rbdProvisionOptions, options controller.VolumeOptions) (*v1.RBDPersistentVolumeSource, int, error) {
	var output []byte
	var err error

	capacity := options.PVC.Spec.Resources.Requests[v1.ResourceName(v1.ResourceStorage)]
	volSizeBytes := capacity.Value()
	// convert to MB that rbd defaults on
	sz := int(util.RoundUpSize(volSizeBytes, 1024*1024))
	if sz <= 0 {
		return nil, 0, fmt.Errorf("invalid storage '%s' requested for RBD provisioner, it must greater than zero", capacity.String())
	}
	volSz := fmt.Sprintf("%d", sz)
	// rbd create
	mon := u.kernelRBDMonitorsOpt(pOpts.monitors)
	if pOpts.imageFormat == rbdImageFormat2 {
		klog.V(4).Infof("rbd: create %s size %s format %s (features: %s) using mon %s, pool %s id %s key %s", image, volSz, pOpts.imageFormat, pOpts.imageFeatures, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
	} else {
		klog.V(4).Infof("rbd: create %s size %s format %s using mon %s, pool %s id %s key %s", image, volSz, pOpts.imageFormat, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
	}
	args := []string{"create", image, "--size", volSz, "--pool", pOpts.pool, "--id", pOpts.adminID, "-m", mon, "--key=" + pOpts.adminSecret, "--image-format", pOpts.imageFormat}
	if pOpts.dataPool != "" {
		args = append(args, "--data-pool", pOpts.dataPool)
	}
	if pOpts.imageFormat == rbdImageFormat2 {
		// if no image features is provided, it results in empty string
		// which disable all RBD image format 2 features as we expected
		features := strings.Join(pOpts.imageFeatures, ",")
		args = append(args, "--image-feature", features)
	}
	output, err = u.execCommand("rbd", args)
	if err != nil {
		klog.Warningf("failed to create rbd image, output %v", string(output))
		return nil, 0, fmt.Errorf("failed to create rbd image: %v, command output: %s", err, string(output))
	}

	return &v1.RBDPersistentVolumeSource{
		CephMonitors: pOpts.monitors,
		RBDImage:     image,
		RBDPool:      pOpts.pool,
		FSType:       pOpts.fsType,
	}, sz, nil
}

// rbdStatus checks if there is watcher on the image.
// It returns true if there is a watcher onthe image, otherwise returns false.
func (u *RBDUtil) rbdStatus(image string, pOpts *rbdProvisionOptions) (bool, error) {
	var err error
	var output string
	var cmd []byte

	mon := u.kernelRBDMonitorsOpt(pOpts.monitors)
	// cmd "rbd status" list the rbd client watch with the following output:
	//
	// # there is a watcher (exit=0)
	// Watchers:
	//   watcher=10.16.153.105:0/710245699 client.14163 cookie=1
	//
	// # there is no watcher (exit=0)
	// Watchers: none
	//
	// Otherwise, exit is non-zero, for example:
	//
	// # image does not exist (exit=2)
	// rbd: error opening image kubernetes-dynamic-pvc-<UUID>: (2) No such file or directory
	//
	klog.V(4).Infof("rbd: status %s using mon %s, pool %s id %s key %s", image, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
	args := []string{"status", image, "--pool", pOpts.pool, "-m", mon, "--id", pOpts.adminID, "--key=" + pOpts.adminSecret}
	cmd, err = u.execCommand("rbd", args)
	output = string(cmd)

	// If command never succeed, returns its last error.
	if err != nil {
		return false, err
	}

	if strings.Contains(output, imageWatcherStr) {
		klog.V(4).Infof("rbd: watchers on %s: %s", image, output)
		return true, nil
	}
	klog.Warningf("rbd: no watchers on %s", image)
	return false, nil
}

// DeleteImage deletes a ceph image with provision and volume options.
func (u *RBDUtil) DeleteImage(image string, pOpts *rbdProvisionOptions) error {
	var output []byte
	found, err := u.rbdStatus(image, pOpts)
	if err != nil {
		return err
	}
	if found {
		klog.Info("rbd is still being used ", image)
		return fmt.Errorf("rbd %s is still being used", image)
	}
	// rbd rm
	mon := u.kernelRBDMonitorsOpt(pOpts.monitors)
	klog.V(4).Infof("rbd: rm %s using mon %s, pool %s id %s key %s", image, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
	args := []string{"rm", image, "--pool", pOpts.pool, "--id", pOpts.adminID, "-m", mon, "--key=" + pOpts.adminSecret}
	output, err = u.execCommand("rbd", args)
	if err == nil {
		return nil
	}
	klog.Errorf("failed to delete rbd image: %v, command output: %s", err, string(output))
	return err
}

func (u *RBDUtil) execCommand(command string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(u.timeout)*time.Second)
	defer cancel()

	// Create the command with our context
	cmd := exec.CommandContext(ctx, command, args...)
	klog.V(4).Infof("Executing command: %v %v", command, args)
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("rbd: Command timed out")
	}

	// If there's no context error, we know the command completed (or errored).
	if err != nil {
		return nil, fmt.Errorf("rbd: Command exited with non-zero code: %v", err)
	}

	return out, err
}
