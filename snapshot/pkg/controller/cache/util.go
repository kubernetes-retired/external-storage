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

/*
Package cache implements data structures used by the attach/detach controller
to keep track of volumes, the nodes they are attached to, and the pods that
reference them.
*/
package cache

import (
	"fmt"
	"strings"
)

// MakeSnapshotName makes a full name for a snapshot that includes
// the namespace and the short name
func MakeSnapshotName(namespace, name string) string {
	return namespace + "/" + name
}

// GetNameAndNameSpaceFromSnapshotName retrieves the namespace and
// the short name of a snapshot from its full name
func GetNameAndNameSpaceFromSnapshotName(name string) (string, string, error) {
	strs := strings.Split(name, "/")
	if len(strs) != 2 {
		return "", "", fmt.Errorf("invalid snapshot name")
	}
	return strs[0], strs[1], nil
}
