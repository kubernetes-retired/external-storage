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
	"fmt"
	"math/rand"
	"os/exec"
	"strings"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/lib/controller"
	"github.com/kubernetes-incubator/external-storage/lib/util"
	"k8s.io/api/core/v1"
)

const (
	imageWatcherStr = "watcher="
)

// RBDUtil is the utility structure to interact with the RBD.
type RBDUtil struct{}

// CreateImage creates a new ceph image with provision and volume options.
func (u *RBDUtil) CreateImage(image string, pOpts *rbdProvisionOptions, options controller.VolumeOptions) (*v1.RBDVolumeSource, int, error) {
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
	l := len(pOpts.monitors)
	// pick a mon randomly
	start := rand.Int() % l
	// iterate all monitors until create succeeds.
	for i := start; i < start+l; i++ {
		mon := pOpts.monitors[i%l]
		if pOpts.imageFormat == rbdImageFormat2 {
			glog.V(4).Infof("rbd: create %s size %s format %s (features: %s) using mon %s, pool %s id %s key %s", image, volSz, pOpts.imageFormat, pOpts.imageFeatures, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
		} else {
			glog.V(4).Infof("rbd: create %s size %s format %s using mon %s, pool %s id %s key %s", image, volSz, pOpts.imageFormat, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
		}
		args := []string{"create", image, "--size", volSz, "--pool", pOpts.pool, "--id", pOpts.adminID, "-m", mon, "--key=" + pOpts.adminSecret, "--image-format", pOpts.imageFormat}
		if pOpts.imageFormat == rbdImageFormat2 {
			// if no image features is provided, it results in empty string
			// which disable all RBD image format 2 features as we expected
			features := strings.Join(pOpts.imageFeatures, ",")
			args = append(args, "--image-feature", features)
		}
		output, err = u.execCommand("rbd", args)
		if err == nil {
			break
		} else {
			glog.Warningf("failed to create rbd image, output %v", string(output))
		}
	}

	if err != nil {
		return nil, 0, fmt.Errorf("failed to create rbd image: %v, command output: %s", err, string(output))
	}

	return &v1.RBDVolumeSource{
		CephMonitors: pOpts.monitors,
		RBDImage:     image,
		RBDPool:      pOpts.pool,
	}, sz, nil
}

// rbdStatus checks if there is watcher on the image.
// It returns true if there is a watcher onthe image, otherwise returns false.
func (u *RBDUtil) rbdStatus(image string, pOpts *rbdProvisionOptions) (bool, error) {
	var err error
	var output string
	var cmd []byte

	l := len(pOpts.monitors)
	start := rand.Int() % l
	// iterate all hosts until mount succeeds.
	for i := start; i < start+l; i++ {
		mon := pOpts.monitors[i%l]
		// cmd "rbd status" list the rbd client watch with the following output:
		// Watchers:
		//   watcher=10.16.153.105:0/710245699 client.14163 cookie=1
		glog.V(4).Infof("rbd: status %s using mon %s, pool %s id %s key %s", image, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
		args := []string{"status", image, "--pool", pOpts.pool, "-m", mon, "--id", pOpts.adminID, "--key=" + pOpts.adminSecret}
		cmd, err = u.execCommand("rbd", args)
		output = string(cmd)

		if err != nil {
			// ignore error code, just checkout output for watcher string
			// TODO: Why should we ignore error code here? Igorning error code here cause we only try first monitor.
			glog.Warningf("failed to execute rbd status on mon %s", mon)
		}

		if strings.Contains(output, imageWatcherStr) {
			glog.V(4).Infof("rbd: watchers on %s: %s", image, output)
			return true, nil
		}
		glog.Warningf("rbd: no watchers on %s", image)
		return false, nil
	}
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
		glog.Info("rbd is still being used ", image)
		return fmt.Errorf("rbd %s is still being used", image)
	}
	// rbd rm
	l := len(pOpts.monitors)
	// pick a mon randomly
	start := rand.Int() % l
	// iterate all monitors until rm succeeds.
	for i := start; i < start+l; i++ {
		mon := pOpts.monitors[i%l]
		glog.V(4).Infof("rbd: rm %s using mon %s, pool %s id %s key %s", image, mon, pOpts.pool, pOpts.adminID, pOpts.adminSecret)
		args := []string{"rm", image, "--pool", pOpts.pool, "--id", pOpts.adminID, "-m", mon, "--key=" + pOpts.adminSecret}
		output, err = u.execCommand("rbd", args)
		if err == nil {
			return nil
		}
		glog.Errorf("failed to delete rbd image: %v, command output: %s", err, string(output))
	}
	return err
}

func (u *RBDUtil) execCommand(command string, args []string) ([]byte, error) {
	cmd := exec.Command(command, args...)
	return cmd.CombinedOutput()
}
