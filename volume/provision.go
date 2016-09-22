package volume

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"

	"github.com/golang/glog"
	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api/v1"
)

// Provision creates a volume i.e. the storage asset and returns a PV object for
// the volume
// TODO upstream does plugin.NewProvisioner and can take advantage of the plugin framework e.g. awsElasticBlockStore has, and uses, manager (.CreateVolume) and plugin (...GetCloudProvider). Find a nicer way to pass the client through the Provisioner?
func Provision(options VolumeOptions, client kubernetes.Interface) (*v1.PersistentVolume, error) {
	// instead of createVolume could call out a script of some kind
	server, path, err := createVolume(options, client)
	if err != nil {
		return nil, err
	}
	pv := &v1.PersistentVolume{
		ObjectMeta: v1.ObjectMeta{
			Name:   options.PVName,
			Labels: map[string]string{},
			Annotations: map[string]string{
				"kubernetes.io/createdby": "nfs-dynamic-provisioner",
			},
		},
		Spec: v1.PersistentVolumeSpec{
			PersistentVolumeReclaimPolicy: options.PersistentVolumeReclaimPolicy,
			AccessModes:                   options.AccessModes,
			Capacity: v1.ResourceList{
				v1.ResourceName(v1.ResourceStorage): options.Capacity,
			},
			PersistentVolumeSource: v1.PersistentVolumeSource{
				NFS: &v1.NFSVolumeSource{
					Server:   server,
					Path:     path,
					ReadOnly: false,
				},
			},
		},
	}

	return pv, nil
}

// createVolume creates a volume i.e. the storage asset. It creates a unique
// directory under /export (which could be the mountpoint of some persistent
// storage or just the ephemeral container directory) and exports it.
func createVolume(options VolumeOptions, client kubernetes.Interface) (string, string, error) {
	// TODO take and validate Parameters
	if options.Parameters != nil {
		return "", "", fmt.Errorf("invalid parameter: no StorageClass parameters are supported")
	}

	// TODO implement options.ProvisionerSelector parsing
	// TODO pv.Labels MUST be set to match claim.spec.selector
	if options.Selector != nil {
		return "", "", fmt.Errorf("claim.Spec.Selector is not supported")
	}

	// TODO quota, something better than just directories
	// TODO figure out permissions: gid, chgrp, root_squash
	path := fmt.Sprintf("/export/%s", options.PVName)
	if _, err := os.Stat(path); err == nil {
		return "", "", fmt.Errorf("error creating volume, the path already exists: %v", err)
	}
	if err := os.MkdirAll(path, 0750); err != nil {
		return "", "", err
	}

	cmd := exec.Command("exportfs", "-o", "rw,no_root_squash,sync", "*:"+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(path)
		return "", "", fmt.Errorf("exportfs -o failed with error: %v, output: %s", err, out)
	}

	server, err := getServer(client)
	if err != nil {
		os.RemoveAll(path)
		return "", "", fmt.Errorf("error getting NFS server IP for created volume: %v", err)
	}

	return server, path, nil
}

func getServer(client kubernetes.Interface) (string, error) {
	// Use either `hostname -i` or MY_POD_IP as the fallback server
	var fallbackServer string
	podIP := os.Getenv("MY_POD_IP")
	if podIP == "" {
		glog.Info("env MY_POD_IP isn't set or provisioner isn't running as a pod")
		out, err := exec.Command("hostname", "-i").Output()
		if err != nil {
			return "", fmt.Errorf("hostname -i failed with error: %v, output: %s", err, out)
		}
		fallbackServer = string(out)
	} else {
		fallbackServer = podIP
	}

	// Try to use the service's cluster IP as the server if MY_SERVICE_NAME is
	// specified. Otherwise, use fallback here.
	serviceName := os.Getenv("MY_SERVICE_NAME")
	if serviceName == "" {
		glog.Info("env MY_SERVICE_NAME isn't set, falling back to using `hostname -i` or pod IP as server IP")
		return fallbackServer, nil
	}

	// From this point forward, rather than fallback & provision non-persistent
	// where persistent is expected, just return an error.
	namespace := os.Getenv("MY_POD_NAMESPACE")
	if namespace == "" {
		return "", fmt.Errorf("env MY_SERVICE_NAME is set but MY_POD_NAMESPACE isn't; no way to get the service cluster IP")
	}
	service, err := client.Core().Services(namespace).Get(serviceName)
	if err != nil {
		return "", fmt.Errorf("error getting service MY_SERVICE_NAME=%s in MY_POD_NAMESPACE=%s", serviceName, namespace)
	}

	// Do some validation of the service before provisioning useless volumes
	valid := false
	expectedPorts := map[int32]v1.Protocol{
		2049:  v1.ProtocolTCP,
		20048: v1.ProtocolTCP,
		// TODO 111 UDP (showmount tries UDP only...)
		111: v1.ProtocolTCP,
	}
	endpoints, err := client.Core().Endpoints(namespace).Get(serviceName)
	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) != 1 {
			continue
		}
		if subset.Addresses[0].IP != fallbackServer {
			continue
		}
		actualPorts := make(map[int32]v1.Protocol)
		for _, port := range subset.Ports {
			actualPorts[port.Port] = port.Protocol
		}
		if !reflect.DeepEqual(expectedPorts, actualPorts) {
			continue
		}
		valid = true
		break
	}
	if !valid {
		return "", fmt.Errorf("service MY_SERVICE_NAME=%s is not valid; check that it has for ports %v one endpoint, this pod's IP %v", serviceName, expectedPorts, fallbackServer)
	}
	if service.Spec.ClusterIP == v1.ClusterIPNone {
		return "", fmt.Errorf("service MY_SERVICE_NAME=%s is valid but it doesn't have a cluster IP", serviceName)
	}

	return service.Spec.ClusterIP, nil
}

func Reprovision() ([]*v1.PersistentVolume, error) {
	return nil, nil
}
