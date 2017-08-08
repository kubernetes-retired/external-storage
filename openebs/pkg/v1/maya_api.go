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

//GetMayaClusterIP returns maya-apiserver IP address
func GetMayaClusterIP(client kubernetes.Interface) string {
	clusterIP := "127.0.0.1"

	//Fetch the Maya ClusterIP using the Maya API Server Service
	sc, err := client.CoreV1().Services("default").Get("maya-apiserver-service", metav1.GetOptions{})
	if err != nil {
		glog.Fatalf("Error getting maya-api-server IP Address: %v", err)
	}

	clusterIP = sc.Spec.ClusterIP
	glog.Infof("Maya Cluster IP: %v", clusterIP)

	return clusterIP
}

// CreateVsm to create the Vsm through a API call to m-apiserver
func CreateVsm(vname string, size string) error {

	var vs mayav1.VsmSpec

	addr := os.Getenv("MAPI_ADDR")
	if addr == "" {
		err := errors.New("MAPI_ADDR environment variable not set")
		glog.Fatalf("Error getting maya-api-server IP Address: %v", err)
		return err
	}
	url := addr + "/latest/volumes/"

	vs.Kind = "PersistentVolumeClaim"
	vs.APIVersion = "v1"
	vs.Metadata.Name = vname
	vs.Metadata.Labels.Storage = size

	//Marshal serializes the value provided into a YAML document
	yamlValue, _ := yaml.Marshal(vs)

	glog.Infof("VSM Spec Created:\n%v\n", string(yamlValue))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(yamlValue))

	req.Header.Add("Content-Type", "application/yaml")

	c := &http.Client{
		Timeout: timeout,
	}
	resp, err := c.Do(req)
	if err != nil {
		glog.Fatalf("http.Do() error: : %v", err)
		return err
	}
	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Fatalf("ioutil.ReadAll() error: : %v", err)
		return err
	}

	code := resp.StatusCode
	if code != http.StatusOK {
		glog.Fatalf("Status error: %v\n", http.StatusText(code))
		return err
	}

	glog.Infof("VSM Successfully Created:\n%v\n", string(data))
	return nil
}

// ListVsm to get the info of Vsm through a API call to m-apiserver
func ListVsm(vname string, obj interface{}) error {

	addr := os.Getenv("MAPI_ADDR")
	if addr == "" {
		err := errors.New("MAPI_ADDR environment variable not set")
		glog.Fatalf("Error getting maya-api-server IP Address: %v", err)
		return err
	}
	url := addr + "/latest/volumes/info/" + vname

	glog.Infof("Get details for VSM :%v", string(vname))

	req, err := http.NewRequest("GET", url, nil)
	c := &http.Client{
		Timeout: timeout,
	}
	resp, err := c.Do(req)
	if err != nil {
		glog.Fatalf("http.Do() error: : %v", err)
		return err
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	if code != http.StatusOK {
		glog.Fatalf("Status error: %v\n", http.StatusText(code))
		return err
	}
	glog.Info("VSM Details Successfully Retrieved")
	return json.NewDecoder(resp.Body).Decode(obj)
}

// DeleteVsm to get delete Vsm through a API call to m-apiserver
func DeleteVsm(vname string) error {

	addr := os.Getenv("MAPI_ADDR")
	if addr == "" {
		err := errors.New("MAPI_ADDR environment variable not set")
		glog.Fatalf("Error getting maya-api-server IP Address: %v", err)
		return err
	}
	url := addr + "/latest/volumes/delete/" + vname

	glog.Infof("Delete VSM :%v", string(vname))

	req, err := http.NewRequest("GET", url, nil)
	c := &http.Client{
		Timeout: timeout,
	}
	resp, err := c.Do(req)
	if err != nil {
		glog.Fatalf("http.Do() error: : %v", err)
		return err
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	if code != http.StatusOK {
		glog.Fatalf("Status error: %v\n", http.StatusText(code))
		return err
	}
	glog.Info("VSM Deleted Successfully initiated")
	return nil
}
