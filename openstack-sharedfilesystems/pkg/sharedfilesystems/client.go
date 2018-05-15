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
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/sharedfilesystems/apiversions"
)

const (
	minimumManilaVersion = "2.21"
)

var (
	microversionRegexp = regexp.MustCompile("^\\d+\\.\\d+$")
)

func splitMicroversion(mv string) (major, minor int) {
	if err := validateMicroversion(mv); err != nil {
		return
	}

	mvParts := strings.Split(mv, ".")
	major, _ = strconv.Atoi(mvParts[0])
	minor, _ = strconv.Atoi(mvParts[1])

	return
}

func validateMicroversion(microversion string) error {
	if !microversionRegexp.MatchString(microversion) {
		return fmt.Errorf("Invalid microversion format in %q", microversion)
	}

	return nil
}

func compareVersionsLessThan(a, b string) bool {
	aMaj, aMin := splitMicroversion(a)
	bMaj, bMin := splitMicroversion(b)

	return aMaj < bMaj || (aMaj == bMaj && aMin < bMin)
}

// NewManilaV2Client Creates Manila v2 client
// Authenticates to the Manila service with credentials passed in env variables
func NewManilaV2Client(allowReauth bool) (*gophercloud.ServiceClient, error) {
	// Authenticate

	regionName := os.Getenv("OS_REGION_NAME")

	authOptions, err := openstack.AuthOptionsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("couldn't retrieve auth options from environment variables: %v", err)
	}

	authOptions.AllowReauth = allowReauth

	provider, err := openstack.AuthenticatedClient(authOptions)
	if err != nil {
		return nil, fmt.Errorf("authentication failed: %v", err)
	}

	client, err := openstack.NewSharedFileSystemV2(provider, gophercloud.EndpointOpts{Region: regionName})
	if err != nil {
		return nil, fmt.Errorf("failed to create Manila v2 client: %v", err)
	}

	// Check client's and server's versions for compatibility

	client.Microversion = minimumManilaVersion

	serverVersion, err := apiversions.Get(client, "v2").Extract()
	if err != nil {
		return nil, fmt.Errorf("failed to get Manila v2 API microversions: %v", err)
	}

	if err = validateMicroversion(serverVersion.MinVersion); err != nil {
		return nil, fmt.Errorf("server's minimum microversion is invalid: %v", err)
	}

	if err = validateMicroversion(serverVersion.Version); err != nil {
		return nil, fmt.Errorf("server's maximum microversion is invalid: %v", err)
	}

	if compareVersionsLessThan(client.Microversion, serverVersion.MinVersion) {
		return nil, fmt.Errorf("client's microversion %s is lower than server's minimum microversion %s", client.Microversion, serverVersion.MinVersion)
	}

	if compareVersionsLessThan(serverVersion.Version, client.Microversion) {
		return nil, fmt.Errorf("client's microversion %s is higher than server's highest supported microversion %s", client.Microversion, serverVersion.Version)
	}

	return client, nil
}
