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
	"fmt"

	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// clusterBroker provides a mechanism for tests to override calls kubernetes with mocks.
type clusterBroker interface {
	createSecret(p *cinderProvisioner, ns string, secret *v1.Secret) error
	deleteSecret(p *cinderProvisioner, ns string, secretName string) error
	getPVC(p *cinderProvisioner, ns string, name string) (*v1.PersistentVolumeClaim, error)
	annotatePVC(p *cinderProvisioner, ns string, name string, updates map[string]string) error
}

type k8sClusterBroker struct {
	clusterBroker
}

func (*k8sClusterBroker) createSecret(p *cinderProvisioner, ns string, secret *v1.Secret) error {
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

func (*k8sClusterBroker) getPVC(p *cinderProvisioner, ns string, name string) (*v1.PersistentVolumeClaim, error) {
	return p.Client.CoreV1().PersistentVolumeClaims(ns).Get(name, metav1.GetOptions{})
}

func (*k8sClusterBroker) annotatePVC(p *cinderProvisioner, ns string, name string, updates map[string]string) error {
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Retrieve the latest version of PVC before attempting update
		// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := p.Client.CoreV1().PersistentVolumeClaims(ns).Get(name, metav1.GetOptions{})
		if getErr != nil {
			panic(fmt.Errorf("Failed to get latest version of PVC: %v", getErr))
		}

		for k, v := range updates {
			result.Annotations[k] = v
		}
		_, updateErr := p.Client.CoreV1().PersistentVolumeClaims(ns).Update(result)
		return updateErr
	})
	if retryErr != nil {
		glog.Errorf("Update failed: %v", retryErr)
		return retryErr
	}
	return nil
}
