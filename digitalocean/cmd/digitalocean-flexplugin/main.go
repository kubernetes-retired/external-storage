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

package main

import (
	"encoding/json"
	"fmt"
	"github.com/digitalocean/godo"
	"github.com/digitalocean/godo/context"
	"golang.org/x/oauth2"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type driverStatus struct {
	// Status of the callout. One of "Success", "Failure" or "Not supported".
	Status string `json:"status"`
	// Reason for success/failure.
	Message string `json:"message,omitempty"`
	// Path to the device attached. This field is valid only for attach calls.
	// ie: /dev/sdx
	DevicePath string `json:"device,omitempty"`
	// Cluster wide unique name of the volume.
	VolumeName string `json:"volumeName,omitempty"`
	// Represents volume is attached on the node
	Attached bool `json:"attached,omitempty"`
	// Returns capabilities of the driver.
	// By default we assume all the capabilities are supported.
	// If the plugin does not support a capability, it can return false for that capability.
	Capabilities driverCapabilities `json:",omitempty"`
}

type driverCapabilities struct {
	Attach         bool `json:"attach"`
	SELinuxRelabel bool `json:"selinuxRelabel"`
}

func findNode(ctx context.Context, client *godo.Client, nodeName string) (int, error) {
	if nodeName == "" {
		// https://github.com/digitalocean/go-metadata/blob/master/client.go#L86
		resp, err := http.Get("http://169.254.169.254/metadata/v1/id")
		if err != nil {
			return 0, err
		}
		var id int
		_, err = fmt.Fscanf(resp.Body, "%d", &id)
		return id, err
	}

	opt := &godo.ListOptions{}
	for {
		droplets, resp, err := client.Droplets.List(ctx, opt)
		if err != nil {
			return 0, err
		}

		for _, d := range droplets {
			if d.Name == nodeName {
				return d.ID, nil
			}
			// Fix gometalinter: declaration of "err" shadows declaration at .. (vetshadow)
			var IP string
			if IP, err = d.PrivateIPv4(); err == nil && IP == nodeName {
				return d.ID, nil
			}
			if IP, err = d.PublicIPv4(); err == nil && IP == nodeName {
				return d.ID, nil
			}
		}

		// if we are at the last page, break out the for loop
		if resp.Links == nil || resp.Links.IsLastPage() {
			break
		}

		page, err := resp.Links.CurrentPage()
		if err != nil {
			return 0, err
		}

		// set the page we want for the next request
		opt.Page = page + 1
	}
	return 0, fmt.Errorf("Error: No droplet matching nodeName %s found", nodeName)
}

func getVolumeByName(ctx context.Context, client *godo.Client, volumeName string) (string, error) {
	volumes, _, err := client.Storage.ListVolumes(ctx, nil)
	if err != nil {
		return "", err
	}
	for _, volume := range volumes {
		if volume.Name == volumeName {
			return volume.ID, nil
		}
	}
	return "", fmt.Errorf("Error: No volume found with volumeName: %s", volumeName)
}

func waitForAction(ctx context.Context, client *godo.Client, action *godo.Action) error {
	completed := false
	for !completed {
		a, _, err := client.Actions.Get(ctx, action.ID)
		if err != nil {
			return err
		}

		switch a.Status {
		case godo.ActionInProgress:
			time.Sleep(5 * time.Second)
		case godo.ActionCompleted:
			completed = true
		default:
			return fmt.Errorf("unknown status: [%s]", a.Status)
		}
	}
	return nil
}

func attach(ctx context.Context, client *godo.Client, options string, nodeName string) (string, error) {
	dropletID, err := findNode(ctx, client, nodeName)
	if err != nil {
		return "", err
	}

	var f struct {
		PvOrVolumeName string `json:"kubernetes.io/pvOrVolumeName"`
	}
	if err = json.Unmarshal([]byte(options), &f); err != nil {
		return "", err
	}
	volumeName := f.PvOrVolumeName

	volumeID, err := getVolumeByName(ctx, client, volumeName)
	if err != nil {
		return "", err
	}

	vol, _, err := client.Storage.GetVolume(ctx, volumeID)
	if err != nil {
		return "", err
	}
	var attached bool
	for _, id := range vol.DropletIDs {
		if id == dropletID {
			attached = true
			break
		} else {
			return "", fmt.Errorf("Error: Volume already attached to: %d", id)
		}
	}

	if !attached {
		action, _, err := client.StorageActions.Attach(ctx, volumeID, dropletID)
		if err != nil {
			return "", err
		}
		if err := waitForAction(ctx, client, action); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("/dev/disk/by-id/scsi-0DO_Volume_%s", volumeName), nil
}

func detach(ctx context.Context, client *godo.Client, options string, nodeName string) error {
	dropletID, err := findNode(ctx, client, nodeName)
	if err != nil {
		return err
	}

	volumeName := options
	volumeID, err := getVolumeByName(ctx, client, volumeName)
	if err != nil {
		return err
	}

	vol, _, err := client.Storage.GetVolume(ctx, volumeID)
	if err != nil {
		return err
	}
	var attached bool
	for _, id := range vol.DropletIDs {
		if id == dropletID {
			attached = true
			break
		} else {
			return fmt.Errorf("Error: The volume %s is attached to another node (%d)", volumeName, id)
		}
	}

	if !attached {
		return nil
	}

	action, _, err := client.StorageActions.DetachByDropletID(ctx, volumeID, dropletID)
	if err != nil {
		return err
	}
	return waitForAction(ctx, client, action)
}

func newDoClient() (*godo.Client, error) {
	doToken, ok := os.LookupEnv("DIGITALOCEAN_ACCESS_TOKEN")

	if !ok {
		dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
		if err != nil {
			return &godo.Client{}, err
		}
		content, err := ioutil.ReadFile(fmt.Sprintf("%s/do_token", dir))
		if err != nil {
			return &godo.Client{}, err
		}

		doToken = strings.Trim(string(content), " \n")
	}

	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: doToken})
	oauthClient := oauth2.NewClient(context.Background(), tokenSource)
	return godo.NewClient(oauthClient), nil
}

func main() {
	ctx := context.TODO()

	doClient, err := newDoClient()
	if err != nil {
		writedriverStatus(driverStatus{Status: "Failure", Message: err.Error()})
		return
	}

	args := os.Args
	argsLen := len(args)
	var command string
	if 1 < argsLen && (argsLen != 2 || args[1] == "init") {
		command = args[1]
		args = args[2:]
		argsLen--
	}
	switch command {
	case "init":
		ds := driverStatus{
			Status: "Success",
			Capabilities: driverCapabilities{
				Attach:         true,
				SELinuxRelabel: true,
			},
		}
		writedriverStatus(ds)
	case "attach":
		var nodeName string
		if argsLen > 2 {
			nodeName = args[1]
		}

		devicePath, err := attach(ctx, doClient, args[0], nodeName)
		if err != nil {
			writedriverStatus(driverStatus{Status: "Failure", Message: err.Error()})
		} else {
			writedriverStatus(driverStatus{Status: "Success", DevicePath: devicePath})
		}
	case "detach":
		var nodeName string
		if argsLen > 2 {
			nodeName = args[1]
		}

		if err := detach(ctx, doClient, args[0], nodeName); err != nil {
			writedriverStatus(driverStatus{Status: "Failure", Message: err.Error()})
		} else {
			writedriverStatus(driverStatus{Status: "Success"})
		}
	default:
		writedriverStatus(driverStatus{Status: "Not supported"})
	}
}

func writedriverStatus(ds driverStatus) error {
	j, err := json.Marshal(ds)
	if err != nil {
		return fmt.Errorf("error encoding driver status to JSON: %s", err.Error())
	}

	fmt.Println(string(j))
	return nil
}
