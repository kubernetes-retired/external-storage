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

package csi

import (
	"fmt"
	"os"
	"testing"
	"time"

	storage "k8s.io/api/storage/v1beta1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	fakeclient "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	utiltesting "k8s.io/client-go/util/testing"
	"k8s.io/kubernetes/pkg/volume"
	volumetest "k8s.io/kubernetes/pkg/volume/testing"
)

func makeTestAttachment(attachID, nodeName, pvName string) *storage.VolumeAttachment {
	return &storage.VolumeAttachment{
		ObjectMeta: meta.ObjectMeta{
			Name: attachID,
		},
		Spec: storage.VolumeAttachmentSpec{
			NodeName: nodeName,
			Attacher: "mock",
			Source: storage.VolumeAttachmentSource{
				PersistentVolumeName: &pvName,
			},
		},
		Status: storage.VolumeAttachmentStatus{
			Attached:    false,
			AttachError: nil,
			DetachError: nil,
		},
	}
}

func TestAttacherAttach(t *testing.T) {

	testCases := []struct {
		name       string
		nodeName   string
		driverName string
		volumeName string
		attachID   string
		shouldFail bool
	}{
		{
			name:       "test ok 1",
			nodeName:   "testnode-01",
			driverName: "testdriver-01",
			volumeName: "testvol-01",
			attachID:   getAttachmentName("testvol-01", "testdriver-01", "testnode-01"),
		},
		{
			name:       "test ok 2",
			nodeName:   "node02",
			driverName: "driver02",
			volumeName: "vol02",
			attachID:   getAttachmentName("vol02", "driver02", "node02"),
		},
		{
			name:       "mismatch vol",
			nodeName:   "node02",
			driverName: "driver02",
			volumeName: "vol01",
			attachID:   getAttachmentName("vol02", "driver02", "node02"),
			shouldFail: true,
		},
		{
			name:       "mismatch driver",
			nodeName:   "node02",
			driverName: "driver000",
			volumeName: "vol02",
			attachID:   getAttachmentName("vol02", "driver02", "node02"),
			shouldFail: true,
		},
		{
			name:       "mismatch node",
			nodeName:   "node000",
			driverName: "driver000",
			volumeName: "vol02",
			attachID:   getAttachmentName("vol02", "driver02", "node02"),
			shouldFail: true,
		},
	}

	// attacher loop
	for i, tc := range testCases {
		t.Logf("test case: %s", tc.name)

		plug, fakeWatcher, tmpDir := newTestWatchPlugin(t)
		defer os.RemoveAll(tmpDir)

		attacher, err := plug.NewAttacher()
		if err != nil {
			t.Fatalf("failed to create new attacher: %v", err)
		}

		csiAttacher := attacher.(*csiAttacher)

		spec := volume.NewSpecFromPersistentVolume(makeTestPV(fmt.Sprintf("test-pv%d", i), 10, tc.driverName, tc.volumeName), false)

		go func(id, nodename string, fail bool) {
			attachID, err := csiAttacher.Attach(spec, types.NodeName(nodename))
			if !fail && err != nil {
				t.Errorf("expecting no failure, but got err: %v", err)
			}
			if attachID != id && !fail {
				t.Errorf("expecting attachID %v, got %v", id, attachID)
			}
		}(tc.attachID, tc.nodeName, tc.shouldFail)

		// update attachment to avoid long waitForAttachment
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		// wait for attachment to be saved
		var attach *storage.VolumeAttachment
		for i := 0; i < 100; i++ {
			attach, err = csiAttacher.k8s.StorageV1beta1().VolumeAttachments().Get(tc.attachID, meta.GetOptions{})
			if err != nil {
				if apierrs.IsNotFound(err) {
					<-ticker.C
					continue
				}
				t.Error(err)
			}
			if attach != nil {
				break
			}
		}

		if attach == nil {
			t.Logf("attachment not found for id:%v", tc.attachID)
		} else {
			attach.Status.Attached = true
			_, err = csiAttacher.k8s.StorageV1beta1().VolumeAttachments().Update(attach)
			if err != nil {
				t.Error(err)
			}
			fakeWatcher.Modify(attach)
		}
	}
}

func TestAttacherWaitForVolumeAttachment(t *testing.T) {

	plug, fakeWatcher, tmpDir := newTestWatchPlugin(t)
	defer os.RemoveAll(tmpDir)

	attacher, err := plug.NewAttacher()
	if err != nil {
		t.Fatalf("failed to create new attacher: %v", err)
	}
	csiAttacher := attacher.(*csiAttacher)
	nodeName := "test-node"

	testCases := []struct {
		name                 string
		initAttached         bool
		finalAttached        bool
		trigerWatchEventTime time.Duration
		initAttachErr        *storage.VolumeError
		finalAttachErr       *storage.VolumeError
		sleepTime            time.Duration
		timeout              time.Duration
		shouldFail           bool
	}{
		{
			name:         "attach success at get",
			initAttached: true,
			sleepTime:    10 * time.Millisecond,
			timeout:      50 * time.Millisecond,
			shouldFail:   false,
		},
		{
			name:          "attachment error ant get",
			initAttachErr: &storage.VolumeError{Message: "missing volume"},
			sleepTime:     10 * time.Millisecond,
			timeout:       30 * time.Millisecond,
			shouldFail:    true,
		},
		{
			name:                 "attach success at watch",
			initAttached:         false,
			finalAttached:        true,
			trigerWatchEventTime: 5 * time.Millisecond,
			timeout:              50 * time.Millisecond,
			sleepTime:            5 * time.Millisecond,
			shouldFail:           false,
		},
		{
			name:                 "attachment error ant watch",
			initAttached:         false,
			finalAttached:        false,
			finalAttachErr:       &storage.VolumeError{Message: "missing volume"},
			trigerWatchEventTime: 5 * time.Millisecond,
			sleepTime:            10 * time.Millisecond,
			timeout:              30 * time.Millisecond,
			shouldFail:           true,
		},
		{
			name:                 "time ran out",
			initAttached:         false,
			finalAttached:        true,
			trigerWatchEventTime: 100 * time.Millisecond,
			timeout:              50 * time.Millisecond,
			sleepTime:            5 * time.Millisecond,
			shouldFail:           true,
		},
	}

	for i, tc := range testCases {
		fakeWatcher.Reset()
		t.Logf("running test: %v", tc.name)
		pvName := fmt.Sprintf("test-pv-%d", i)
		volID := fmt.Sprintf("test-vol-%d", i)
		attachID := getAttachmentName(volID, testDriver, nodeName)
		attachment := makeTestAttachment(attachID, nodeName, pvName)
		attachment.Status.Attached = tc.initAttached
		attachment.Status.AttachError = tc.initAttachErr
		csiAttacher.waitSleepTime = tc.sleepTime
		_, err := csiAttacher.k8s.StorageV1beta1().VolumeAttachments().Create(attachment)
		if err != nil {
			t.Fatalf("failed to attach: %v", err)
		}

		// after timeout, fakeWatcher will be closed by csiAttacher.waitForVolumeAttachment
		if tc.trigerWatchEventTime > 0 && tc.trigerWatchEventTime < tc.timeout {
			go func() {
				time.Sleep(tc.trigerWatchEventTime)
				attachment.Status.Attached = tc.finalAttached
				attachment.Status.AttachError = tc.finalAttachErr
				fakeWatcher.Modify(attachment)
			}()
		}

		retID, err := csiAttacher.waitForVolumeAttachment(volID, attachID, tc.timeout)
		if tc.shouldFail && err == nil {
			t.Error("expecting failure, but err is nil")
		}
		if tc.initAttachErr != nil {
			if tc.initAttachErr.Message != err.Error() {
				t.Errorf("expecting error [%v], got [%v]", tc.initAttachErr.Message, err.Error())
			}
		}
		if err == nil && retID != attachID {
			t.Errorf("attacher.WaitForAttach not returning attachment ID")
		}
	}
}

func TestAttacherVolumesAreAttached(t *testing.T) {
	plug, tmpDir := newTestPlugin(t)
	defer os.RemoveAll(tmpDir)

	attacher, err := plug.NewAttacher()
	if err != nil {
		t.Fatalf("failed to create new attacher: %v", err)
	}
	csiAttacher := attacher.(*csiAttacher)
	nodeName := "test-node"

	testCases := []struct {
		name          string
		attachedStats map[string]bool
	}{
		{"attach + detach", map[string]bool{"vol-01": true, "vol-02": true, "vol-03": false, "vol-04": false, "vol-05": true}},
		{"all detached", map[string]bool{"vol-11": false, "vol-12": false, "vol-13": false, "vol-14": false, "vol-15": false}},
		{"all attached", map[string]bool{"vol-21": true, "vol-22": true, "vol-23": true, "vol-24": true, "vol-25": true}},
	}

	for _, tc := range testCases {
		var specs []*volume.Spec
		// create and save volume attchments
		for volName, stat := range tc.attachedStats {
			pv := makeTestPV("test-pv", 10, testDriver, volName)
			spec := volume.NewSpecFromPersistentVolume(pv, pv.Spec.PersistentVolumeSource.CSI.ReadOnly)
			specs = append(specs, spec)
			attachID := getAttachmentName(volName, testDriver, nodeName)
			attachment := makeTestAttachment(attachID, nodeName, pv.GetName())
			attachment.Status.Attached = stat
			_, err := csiAttacher.k8s.StorageV1beta1().VolumeAttachments().Create(attachment)
			if err != nil {
				t.Fatalf("failed to attach: %v", err)
			}
		}

		// retrieve attached status
		stats, err := csiAttacher.VolumesAreAttached(specs, types.NodeName(nodeName))
		if err != nil {
			t.Fatal(err)
		}
		if len(tc.attachedStats) != len(stats) {
			t.Errorf("expecting %d attachment status, got %d", len(tc.attachedStats), len(stats))
		}

		// compare attachment status for each spec
		for spec, stat := range stats {
			source, err := getCSISourceFromSpec(spec)
			if err != nil {
				t.Error(err)
			}
			if stat != tc.attachedStats[source.VolumeHandle] {
				t.Errorf("expecting volume attachment %t, got %t", tc.attachedStats[source.VolumeHandle], stat)
			}
		}
	}
}

func TestAttacherDetach(t *testing.T) {

	nodeName := "test-node"
	testCases := []struct {
		name       string
		volID      string
		attachID   string
		shouldFail bool
	}{
		{name: "normal test", volID: "vol-001", attachID: getAttachmentName("vol-001", testDriver, nodeName)},
		{name: "normal test 2", volID: "vol-002", attachID: getAttachmentName("vol-002", testDriver, nodeName)},
		{name: "object not found", volID: "vol-001", attachID: getAttachmentName("vol-002", testDriver, nodeName), shouldFail: true},
	}

	for _, tc := range testCases {
		t.Logf("running test: %v", tc.name)
		plug, fakeWatcher, tmpDir := newTestWatchPlugin(t)
		defer os.RemoveAll(tmpDir)

		attacher, err0 := plug.NewAttacher()
		if err0 != nil {
			t.Fatalf("failed to create new attacher: %v", err0)
		}
		csiAttacher := attacher.(*csiAttacher)

		pv := makeTestPV("test-pv", 10, testDriver, tc.volID)
		spec := volume.NewSpecFromPersistentVolume(pv, pv.Spec.PersistentVolumeSource.CSI.ReadOnly)
		attachment := makeTestAttachment(tc.attachID, nodeName, "test-pv")
		_, err := csiAttacher.k8s.StorageV1beta1().VolumeAttachments().Create(attachment)
		if err != nil {
			t.Fatalf("failed to attach: %v", err)
		}
		volumeName, err := plug.GetVolumeName(spec)
		if err != nil {
			t.Errorf("test case %s failed: %v", tc.name, err)
		}
		go func() {
			fakeWatcher.Delete(attachment)
		}()
		err = csiAttacher.Detach(volumeName, types.NodeName(nodeName))
		if tc.shouldFail && err == nil {
			t.Fatal("expecting failure, but err = nil")
		}
		if !tc.shouldFail && err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		attach, err := csiAttacher.k8s.StorageV1beta1().VolumeAttachments().Get(tc.attachID, meta.GetOptions{})
		if err != nil {
			if !apierrs.IsNotFound(err) {
				t.Fatalf("unexpected err: %v", err)
			}
		} else {
			if attach == nil {
				t.Errorf("expecting attachment not to be nil, but it is")
			}
		}
	}
}

// create a plugin mgr to load plugins and setup a fake client
func newTestWatchPlugin(t *testing.T) (*csiPlugin, *watch.FakeWatcher, string) {
	tmpDir, err := utiltesting.MkTmpdir("csi-test")
	if err != nil {
		t.Fatalf("can't create temp dir: %v", err)
	}

	fakeClient := fakeclient.NewSimpleClientset()
	fakeWatcher := watch.NewFake()
	fakeClient.Fake.PrependWatchReactor("*", core.DefaultWatchReactor(fakeWatcher, nil))
	fakeClient.Fake.WatchReactionChain = fakeClient.Fake.WatchReactionChain[:1]
	host := volumetest.NewFakeVolumeHost(
		tmpDir,
		fakeClient,
		nil,
	)
	plugMgr := &volume.VolumePluginMgr{}
	plugMgr.InitPlugins(ProbeVolumePlugins(), nil /* prober */, host)

	plug, err := plugMgr.FindPluginByName(csiPluginName)
	if err != nil {
		t.Fatalf("can't find plugin %v", csiPluginName)
	}

	csiPlug, ok := plug.(*csiPlugin)
	if !ok {
		t.Fatalf("cannot assert plugin to be type csiPlugin")
	}

	return csiPlug, fakeWatcher, tmpDir
}
