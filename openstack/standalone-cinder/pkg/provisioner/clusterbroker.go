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

package provisioner

import (
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
)

// clusterBroker provides a mechanism for tests to override calls kubernetes with mocks.
type clusterBroker interface {
	createSecret(p *cinderProvisioner, ns string, secret *v1.Secret) error
	deleteSecret(p *cinderProvisioner, ns string, secretName string) error
}

type k8sClusterBroker struct {
	clusterBroker
}

func (k8sClusterBroker) createSecret(p *cinderProvisioner, ns string, secret *v1.Secret) error {
	_, err := p.Client.CoreV1().Secrets(ns).Create(secret)
	if err != nil {
		glog.Errorf("Failed to create chap secret in namespace %s: %v", ns, err)
		return err
	}
	glog.V(3).Infof("Secret %s created", secret.ObjectMeta.Name)
	return nil
}

func (*k8sClusterBroker) deleteSecret(p *cinderProvisioner, ns string, secretName string) error {
	err := p.Client.CoreV1().Secrets(ns).Delete(secretName, nil)
	if err != nil {
		glog.Errorf("Failed to remove secret %s from namespace %s: %v", secretName, ns, err)
		return err
	}
	glog.V(3).Infof("Successfully deleted secret %s", secretName)
	return nil
}
