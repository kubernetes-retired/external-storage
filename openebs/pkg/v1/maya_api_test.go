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

package v1

import (
	"errors"
	"reflect"
	"testing"

	mayav1 "github.com/kubernetes-incubator/external-storage/openebs/types/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type MayaClusterIPTest struct {
	ClusterIP string
	Err       error
}

type OpenEBSVolumeTest struct {
	Message string
	Err     error
}

func (mIP MayaClusterIPTest) GetMayaClusterIP(client kubernetes.Interface) (string, error) {
	if mIP.Err != nil {
		return "", mIP.Err
	}
	return mIP.ClusterIP, nil
}

func (ov OpenEBSVolumeTest) CreateVolume(mayav1.VolumeSpec) (string, error) {
	if ov.Err != nil {
		return ov.Message, ov.Err
	}
	return ov.Message, nil
}

func (ov OpenEBSVolumeTest) ListVolume(vaname string, size interface{}) error {
	if ov.Err != nil {
		return ov.Err
	}
	return nil
}

func (ov OpenEBSVolumeTest) DeleteVolume(vname string) error {
	if ov.Err != nil {
		return ov.Err
	}
	return nil
}

func EvaluateGetMayaClusterIP(mi MayaInterface) (string, error) {
	client := fake.NewSimpleClientset()
	return mi.GetMayaClusterIP(client)
}

func EvaluateCreateVolume(ovi OpenEBSVolumeInterface, vs mayav1.VolumeSpec) (string, error) {
	return ovi.CreateVolume(vs)
}

func EvaluateListVolume(ovi OpenEBSVolumeInterface, vname string, obj interface{}) error {
	return ovi.ListVolume(vname, obj)
}

func EvaluateDeleteVolume(ovi OpenEBSVolumeInterface, vname string) error {
	return ovi.DeleteVolume(vname)
}

func TestGetMayaClusterIP(t *testing.T) {
	cases := []struct {
		mIP         MayaClusterIPTest
		expectedIP  string
		expectedErr error
	}{
		{
			mIP: MayaClusterIPTest{

				ClusterIP: "127.0.0.1:5656",
				Err:       nil,
			},
			expectedIP:  "127.0.0.1:5656",
			expectedErr: nil,
		},
	}

	for _, c := range cases {
		clusterIP, err := EvaluateGetMayaClusterIP(c.mIP)

		if c.expectedErr != err {
			t.Fatalf("Expected err to be in the nil but it was %s", err)
		}
		if c.expectedIP != clusterIP {
			t.Fatalf("Expected %s but got %s", c.expectedIP, clusterIP)
		}

	}

}

func TestCreateVsm(t *testing.T) {
	cases := []struct {
		name            string
		vname           string
		size            string
		ovt             OpenEBSVolumeTest
		expectedErr     error
		expectedMessage string
	}{{
		name:  "Volume creation: Success",
		vname: "vol-01",
		size:  "5Gi",
		ovt: OpenEBSVolumeTest{
			Message: "Volume Created Successfully",
			Err:     nil,
		},
		expectedErr:     nil,
		expectedMessage: "Volume Created Successfully",
	},
	}

	for _, c := range cases {
		vs := mayav1.VolumeSpec{}
		vs.Metadata.Name = c.vname
		vs.Metadata.Labels.Storage = c.size
		message, err := EvaluateCreateVolume(c.ovt, c.vname, c.size)

		if !reflect.DeepEqual(err, c.expectedErr) {
			t.Fatalf("Expected error %v got %v", c.expectedErr, err)
		}
		if message != c.expectedMessage {
			t.Fatalf("Expected message %s , got %s", c.expectedMessage, message)
		}
	}
}

func TestListVolume(t *testing.T) {
	cases := []struct {
		name        string
		vname       string
		ovt         OpenEBSVolumeTest
		expectedErr error
	}{
		{
			name:  "Test for list vsm",
			vname: "vol-01",
			ovt: OpenEBSVolumeTest{
				Message: "",
				Err:     nil,
			},
			expectedErr: nil,
		},

		{
			name:  "Test for list vsm",
			vname: "vol-01",
			ovt: OpenEBSVolumeTest{
				Message: "",
				Err:     errors.New("No VSMs"),
			},
			expectedErr: errors.New("No VSMs"),
		},
	}

	for _, c := range cases {
		err := EvaluateListVolume(c.ovt, c.vname, nil)
		if !reflect.DeepEqual(err, c.expectedErr) {
			t.Fatalf("Expected error %v  got %v", c.expectedErr, err)
		}
	}
}

func TestDeleteVsm(t *testing.T) {
	cases := []struct {
		testName    string
		vname       string
		ovt         OpenEBSVolumeTest
		expectedErr error
	}{
		{
			testName: "Test for delete vsm",
			vname:    "vol-01",
			ovt: OpenEBSVolumeTest{
				Message: "",
				Err:     nil,
			},
			expectedErr: nil,
		},

		{
			testName: "Test for delete vsm",
			vname:    "vol-01",
			ovt: OpenEBSVolumeTest{
				Message: "",
				Err:     errors.New("No VSMs to delete"),
			},
			expectedErr: errors.New("No VSMs to delete"),
		},
	}

	for _, c := range cases {
		err := EvaluateListVolume(c.ovt, c.vname, nil)
		if !reflect.DeepEqual(err, c.expectedErr) {
			t.Fatalf("Expected error %v  got %v", c.expectedErr, err)
		}
	}
}
