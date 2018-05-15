/*
Copyright 2018 The Kubernetes Authors.

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

package sharedfilesystems

import (
	"fmt"

	"github.com/kubernetes-incubator/external-storage/openstack-sharedfilesystems/pkg/sharedfilesystems/sharebackends"
)

var (
	shareBackends    map[string]sharebackends.ShareBackend
	shareBackendsMap map[string]string // PV name -> Backend name
)

func init() {
	shareBackends = make(map[string]sharebackends.ShareBackend)
	shareBackendsMap = make(map[string]string)

	// Register all share backends here:

	registerShareBackend(&sharebackends.NFS{})
	registerShareBackend(&sharebackends.CSICephFS{})
}

func getShareBackend(backendName string) (sharebackends.ShareBackend, error) {
	if b, ok := shareBackends[backendName]; !ok {
		return nil, fmt.Errorf("share backend %s not found", backendName)
	} else {
		return b, nil
	}
}

func getBackendNameForShare(shareName string) (string, error) {
	if backendName, ok := shareBackendsMap[shareName]; !ok {
		return "", fmt.Errorf("no backend registered for share %s", shareName)
	} else {
		return backendName, nil
	}
}

func registerShareBackend(b sharebackends.ShareBackend) {
	shareBackends[b.Name()] = b
}

func registerBackendForShare(backendName, shareName string) {
	shareBackendsMap[shareName] = backendName
}
