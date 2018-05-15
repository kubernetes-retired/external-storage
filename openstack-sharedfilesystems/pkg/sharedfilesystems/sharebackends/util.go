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

package sharebackends

import (
	"fmt"
	"strings"

	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/v2/shares"
)

func getSecretName(shareID string) string {
	return "manila-" + shareID
}

// Splits ExportLocation path "addr1:port,addr2:port,...:/location" into its address
// and location parts. The last occurance of ':' is considered as the delimiter
// between those two parts.
func splitExportLocation(loc *shares.ExportLocation) (address, location string, err error) {
	delimPos := strings.LastIndexByte(loc.Path, ':')
	if delimPos <= 0 {
		err = fmt.Errorf("failed to parse address and location from export location '%s'", loc.Path)
		return
	}

	address = loc.Path[:delimPos]
	location = loc.Path[delimPos+1:]

	return
}
