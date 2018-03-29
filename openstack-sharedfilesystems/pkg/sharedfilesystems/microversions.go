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

// Functions in this file shall ideally be moved into the github.com/gophercloud/gophercloud library

package sharedfilesystems

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SplitMicroversion splits OpenStack microversion represented as string into Major and Minor versions represented as ints
func SplitMicroversion(mv string) (major, minor int) {
	if err := ValidMicroversion(mv); err != nil {
		return
	}

	mvParts := strings.Split(mv, ".")
	major, _ = strconv.Atoi(mvParts[0])
	minor, _ = strconv.Atoi(mvParts[1])

	return
}

// ValidMicroversion checks whether the microversion provided as a string is a valid OpenStack microversion
func ValidMicroversion(mv string) (err error) {
	mvRe := regexp.MustCompile("^\\d+\\.\\d+$")
	if v := mvRe.MatchString(mv); v {
		return
	}

	err = fmt.Errorf("invalid microversion: %q", mv)
	return
}
