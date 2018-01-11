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

package provision

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
)

// SupportedImageFeatures is a set of image features
type SupportedImageFeatures struct {
	featuresSupported sets.String
}

var allowedImageFeatures = newAllowedSupportedImageFeatures()

func newAllowedSupportedImageFeatures() SupportedImageFeatures {
	return SupportedImageFeatures{
		featuresSupported: sets.NewString("layering", "striping", "exclusive-lock", "object-map", "fast-diff", "deep-flatten", "journaling"),
	}
}

// NewDefaultSupportedImageFeatures creates a set of default image features
func NewDefaultSupportedImageFeatures() SupportedImageFeatures {
	return SupportedImageFeatures{featuresSupported: sets.NewString("layering", "striping")}
}

// String returns the list of image features comma-separated (use by flag)
func (sif SupportedImageFeatures) String() string {
	return strings.Join(sif.featuresSupported.UnsortedList(), ", ")
}

// Set add a new image feature to to set (use by flag)
func (sif SupportedImageFeatures) Set(value string) error {
	if value != "" {
		values := strings.Split(value, ",")
		for _, v := range values {
			if !allowedImageFeatures.IsSupported(v) {
				return fmt.Errorf("invalid image feature '%s', supported image features are: %s", v, allowedImageFeatures.String())
			}
			sif.featuresSupported.Insert(v)
		}
	}
	return nil
}

// List returns an array of image features (as string)
func (sif SupportedImageFeatures) List() []string {
	return sif.featuresSupported.UnsortedList()
}

// IsSupported check if an image feature is valid
func (sif SupportedImageFeatures) IsSupported(f string) bool {
	return sif.featuresSupported.Has(f)
}
