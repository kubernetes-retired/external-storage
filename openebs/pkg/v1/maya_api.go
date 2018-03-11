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

package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/golang/glog"
	mayav1 "github.com/kubernetes-incubator/external-storage/openebs/types/v1"
	yaml "gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	timeout = 60 * time.Second
)

//OpenEBSVolumeInterface Interface to bind methods
type OpenEBSVolumeInterface interface {
	CreateVolume(mayav1.VolumeSpec) (string, error)
	ListVolume(string, interface{}) error
	DeleteVolume(string) error
}

//MayaInterface interface to hold maya specific methods
type MayaInterface interface {
	GetMayaClusterIP(kubernetes.Interface) (string, error)
}

//OpenEBSVolume struct
type OpenEBSVolume struct{}

//GetMayaClusterIP returns maya-apiserver IP address
func (v OpenEBSVolume) GetMayaClusterIP(client kubernetes.Interface) (string, error) {
	clusterIP := "127.0.0.1"

	namespace := os.Getenv("OPENEBS_NAMESPACE")
	if namespace == "" {
		namespace = "default"
	}

	glog.Info("OpenEBS volume provisioner namespace ", namespace)
	//Fetch the Maya ClusterIP using the Maya API Server Service
	sc, err := client.CoreV1().Services(namespace).Get("maya-apiserver-service", metav1.GetOptions{})
	if err != nil {
		glog.Errorf("Error getting maya-apiserver IP Address: %v", err)
	}

	clusterIP = sc.Spec.ClusterIP
	glog.V(2).Infof("Maya Cluster IP: %v", clusterIP)

	return clusterIP, err
}

// CreateVolume to create the Vsm through a API call to m-apiserver
func (v OpenEBSVolume) CreateVolume(vs mayav1.VolumeSpec) (string, error) {

	addr := os.Getenv("MAPI_ADDR")
	if addr == "" {
		err := errors.New("MAPI_ADDR environment variable not set")
		glog.Errorf("Error getting maya-apiserver IP Address: %v", err)
		return "Error getting maya-apiserver IP Address", err
	}
	url := addr + "/latest/volumes/"

	vs.Kind = "PersistentVolumeClaim"
	vs.APIVersion = "v1"

	//Marshal serializes the value provided into a YAML document
	yamlValue, _ := yaml.Marshal(vs)

	glog.V(2).Infof("[DEBUG] volume Spec Created:\n%v\n", string(yamlValue))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(yamlValue))

	req.Header.Add("Content-Type", "application/yaml")

	c := &http.Client{
		Timeout: timeout,
	}
	resp, err := c.Do(req)
	if err != nil {
		glog.Errorf("Error when connecting maya-apiserver %v", err)
		return "Could not connect to maya-apiserver", err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorf("Unable to read response from maya-apiserver %v", err)
		return "Unable to read response from maya-apiserver", err
	}

	code := resp.StatusCode
	if code != http.StatusOK {
		glog.Errorf("Status error: %v\n", http.StatusText(code))
		return "HTTP Status error from maya-apiserver", err
	}

	glog.Infof("volume Successfully Created:\n%v\n", string(data))
	return "volume Successfully Created", nil
}

// ListVolume to get the info of Vsm through a API call to m-apiserver
func (v OpenEBSVolume) ListVolume(vname string, obj interface{}) error {

	addr := os.Getenv("MAPI_ADDR")
	if addr == "" {
		err := errors.New("MAPI_ADDR environment variable not set")
		glog.Errorf("Error getting mayaapi-server IP Address: %v", err)
		return err
	}
	url := addr + "/latest/volumes/info/" + vname

	glog.V(2).Infof("[DEBUG] Get details for Volume :%v", string(vname))

	req, err := http.NewRequest("GET", url, nil)
	c := &http.Client{
		Timeout: timeout,
	}
	resp, err := c.Do(req)
	if err != nil {
		glog.Errorf("Error when connecting to maya-apiserver %v", err)
		return err
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	if code != http.StatusOK {
		glog.Errorf("HTTP Status error from maya-apiserver: %v\n", http.StatusText(code))
		return err
	}
	glog.V(2).Info("volume Details Successfully Retrieved")
	return json.NewDecoder(resp.Body).Decode(obj)
}

// DeleteVolume to get delete Vsm through a API call to m-apiserver
func (v OpenEBSVolume) DeleteVolume(vname string) error {

	addr := os.Getenv("MAPI_ADDR")
	if addr == "" {
		err := errors.New("MAPI_ADDR environment variable not set")
		glog.Errorf("Error getting maya-api-server IP Address: %v", err)
		return err
	}
	url := addr + "/latest/volumes/delete/" + vname

	glog.V(2).Infof("[DEBUG] Delete Volume :%v", string(vname))

	req, err := http.NewRequest("GET", url, nil)
	c := &http.Client{
		Timeout: timeout,
	}
	resp, err := c.Do(req)
	if err != nil {
		glog.Errorf("Error when connecting to maya-apiserver  %v", err)
		return err
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	if code != http.StatusOK {
		glog.Errorf("HTTP Status error from maya-apiserver: %v\n", http.StatusText(code))
		return err
	}
	glog.Info("volume Deleted Successfully initiated")
	return nil
}
