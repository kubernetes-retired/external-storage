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

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/external-storage/local-volume/utils/update-helm-values-pre-v2.2.0/pkg/chartutil"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	optEngine        string
	optInput         string
	optOutput        string
	supportedEngines = sets.NewString(
		"baremetal",
		"gcePre19",
		"gcePost19",
		"gke",
	)
)

func init() {
	flag.StringVar(&optInput, "input", "", "values file to update")
	flag.StringVar(&optOutput, "output", "-", "values file to write out")
	flag.StringVar(&optEngine, "engine", "baremetal", "engine")
}

func isNoValueError(err error) bool {
	_, ok := err.(chartutil.ErrNoValue)
	return ok
}

func isNoTableError(err error) bool {
	_, ok := err.(chartutil.ErrNoTable)
	return ok
}

func upgrade(val chartutil.Values, engine string) (chartutil.Values, error) {
	out := val
	// 1. Move configmap.configMapName to common.configMapName
	configMapName, err := val.PathValue("configmap.configMapName")
	if err != nil && !isNoValueError(err) {
		return out, err
	}
	if err == nil {
		outCommon, err1 := out.Table("common")
		if err1 != nil && !isNoTableError(err1) {
			return out, err1
		}
		if isNoTableError(err1) {
			outCommon = chartutil.Values{}
			out["common"] = outCommon
		}
		outCommon["configMapName"] = configMapName
	}
	// 2. Replace configmap with classes
	classes := make([]chartutil.Values, 0)
	key := fmt.Sprintf("configmap.%s.storageClass", engine)
	storageClass, err := val.PathValue(key)
	if err != nil {
		return out, err
	}
	storageClassList, ok := storageClass.([]interface{})
	if !ok {
		return out, fmt.Errorf("invalid values")
	}
	for _, class := range storageClassList {
		classMap, ok := class.(map[string]interface{})
		if !ok {
			return out, fmt.Errorf("invalid values")
		}
		for name, val := range classMap {
			classValue := chartutil.Values{
				"name": name,
			}
			if kvs, ok := val.(map[string]interface{}); ok {
				for k, v := range kvs {
					classValue[k] = v
				}
			}
			classes = append(classes, classValue)
		}
	}
	out["classes"] = classes
	delete(out, "configmap")
	// 3. Set daemonset.imagePullPolicy if needed
	imagePullPolicy, err := val.PathValue("daemonset.imagePullPolicy")
	if err != nil && !isNoValueError(err) {
		return out, err
	}
	if v, ok := imagePullPolicy.(string); (ok && v == "") || isNoValueError(err) {
		daemonset, err := out.Table("daemonset")
		if err != nil && !isNoTableError(err) {
			return out, err
		}
		if isNoTableError(err) {
			daemonset = chartutil.Values{}
			out["daemonset"] = daemonset
		}
		daemonset["imagePullPolicy"] = "Always"
	}
	return out, nil
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	if optInput == "" || optOutput == "" {
		flag.Usage()
		return
	}

	if !supportedEngines.Has(optEngine) {
		glog.Errorf("not supported engine: %s, available: %v", optEngine, supportedEngines.List())
		return
	}

	var (
		err error
		out *os.File
	)

	if optOutput == "-" {
		out = os.Stdout
	} else {
		out, err = os.OpenFile(optOutput, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
		if err != nil {
			glog.Fatal(err)
		}
	}

	inVal, err := chartutil.ReadValuesFile(optInput)
	if err != nil {
		glog.Fatal(err)
	}

	outVal, err := upgrade(inVal, optEngine)
	if err != nil {
		glog.Fatal(err)
	}

	outStr, err := outVal.YAML()
	if err != nil {
		glog.Fatal(err)
	}
	out.WriteString(outStr)
}
