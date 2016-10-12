/*
Copyright 2016 Red Hat, Inc.

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
	"fmt"
	"strings"

	"github.com/golang/glog"
	"github.com/openshift/origin/pkg/security/uid"
	"k8s.io/client-go/1.4/pkg/api/v1"
	"k8s.io/client-go/1.4/pkg/apis/extensions/v1beta1"
)

const (
	UIDRangeAnnotation = "openshift.io/sa.scc.uid-range"
	// SupplementalGroupsAnnotation contains a comma delimited list of allocated supplemental groups
	// for the namespace.  Groups are in the form of a Block which supports {start}/{length} or {start}-{end}
	SupplementalGroupsAnnotation = "openshift.io/sa.scc.supplemental-groups"
)

// getSupplementalGroupsAnnotation provides a backwards compatible way to get supplemental groups
// annotations from a namespace by looking for SupplementalGroupsAnnotation and falling back to
// UIDRangeAnnotation if it is not found.
// openshift/origin pkg/security/scc/matcher.go
func getSupplementalGroupsAnnotation(ns *v1.Namespace) (string, error) {
	groups, ok := ns.Annotations[SupplementalGroupsAnnotation]
	if !ok {
		glog.V(4).Infof("unable to find supplemental group annotation %s falling back to %s", SupplementalGroupsAnnotation, UIDRangeAnnotation)

		groups, ok = ns.Annotations[UIDRangeAnnotation]
		if !ok {
			return "", fmt.Errorf("unable to find supplemental group or uid annotation for namespace %s", ns.Name)
		}
	}

	if len(groups) == 0 {
		return "", fmt.Errorf("unable to find groups using %s and %s annotations", SupplementalGroupsAnnotation, UIDRangeAnnotation)
	}
	return groups, nil
}

// getPreallocatedSupplementalGroups gets the annotated value from the namespace.
// openshift/origin pkg/security/scc/matcher.go
func getPreallocatedSupplementalGroups(ns *v1.Namespace) ([]v1beta1.IDRange, error) {
	groups, err := getSupplementalGroupsAnnotation(ns)
	if err != nil {
		return nil, err
	}
	glog.V(4).Infof("got preallocated value for groups: %s in namespace %s", groups, ns.Name)

	blocks, err := parseSupplementalGroupAnnotation(groups)
	if err != nil {
		return nil, err
	}

	idRanges := []v1beta1.IDRange{}
	for _, block := range blocks {
		rng := v1beta1.IDRange{
			Min: int64(block.Start),
			Max: int64(block.End),
		}
		idRanges = append(idRanges, rng)
	}
	return idRanges, nil
}

// parseSupplementalGroupAnnotation parses the group annotation into blocks.
// openshift/origin pkg/security/scc/matcher.go
func parseSupplementalGroupAnnotation(groups string) ([]uid.Block, error) {
	blocks := []uid.Block{}
	segments := strings.Split(groups, ",")
	for _, segment := range segments {
		block, err := uid.ParseBlock(segment)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no blocks parsed from annotation %s", groups)
	}
	return blocks, nil
}
