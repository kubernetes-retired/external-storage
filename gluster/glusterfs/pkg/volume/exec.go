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
	"bytes"
	"fmt"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiRemotecommand "k8s.io/apimachinery/pkg/util/remotecommand"
	"k8s.io/client-go/tools/remotecommand"
)

func (p *glusterfsProvisioner) ExecuteCommands(host string,
	commands []string,
	config *ProvisionerConfig) error {

	pod, err := p.selectPod(host, config)
	if err != nil {
		return err
	}
	for _, command := range commands {
		err := p.ExecuteCommand(command, pod)
		if err != nil {
			return err
		}
	}

	return nil
}

func (p *glusterfsProvisioner) ExecuteCommand(
	command string,
	pod *v1.Pod) error {
	glog.V(4).Infof("Pod: %s, ExecuteCommand: %s", pod.Name, command)

	containerName := pod.Spec.Containers[0].Name
	req := p.restClient.Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		Param("container", containerName).
		Param("stdout", "true").
		Param("stderr", "true")

	for _, c := range []string{"/bin/bash", "-c", command} {
		req.Param("command", c)
	}

	exec, err := remotecommand.NewExecutor(p.config, "POST", req.URL())
	if err != nil {
		glog.Fatalf("Failed to create NewExecutor: %v", err)
		return err
	}

	var b bytes.Buffer
	var berr bytes.Buffer

	err = exec.Stream(remotecommand.StreamOptions{
		SupportedProtocols: apiRemotecommand.SupportedStreamingProtocols,
		Stdout:             &b,
		Stderr:             &berr,
		Tty:                false,
	})

	glog.Infof("Result: %v", b.String())
	glog.Infof("Result: %v", berr.String())
	if err != nil {
		glog.Errorf("Failed to create Stream: %v", err)
		return err
	}

	return nil
}

func (p *glusterfsProvisioner) selectPod(host string,
	config *ProvisionerConfig) (*v1.Pod, error) {

	podList, err := p.client.Core().
		Pods(config.Namespace).
		List(meta_v1.ListOptions{
			LabelSelector: config.LabelSelector,
		})
	if err != nil {
		return nil, err
	}
	pods := podList.Items
	if len(pods) == 0 {
		return nil, fmt.Errorf("No pods found for glusterfs, LabelSelector: %v", config.LabelSelector)
	}
	for _, pod := range pods {
		if pod.Status.PodIP == host {
			glog.Infof("Pod selecterd: %v/%v\n", pod.Namespace, pod.Name)
			return &pod, nil
		}
	}

	return nil, fmt.Errorf("No pod found to match NodeName == %s", host)
}
