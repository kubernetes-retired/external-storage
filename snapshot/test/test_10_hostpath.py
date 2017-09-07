# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import unittest
import snaptestcase
import json
import time
import os

class SnapHostpathTest(snaptestcase.SnapTestCase):
    '''Basic HostPath based tests'''
    def test_10_create(self):
        """
        Test creation of a PVC snapshot
        """
        # testing yaml files
        classyaml = self.findfile('hostpath_files/hostpath-class.yaml')
        pvyaml = self.findfile('hostpath_files/hostpath-pv.yaml')
        pvcyaml = self.findfile('hostpath_files/hostpath-pvc.yaml')
        snapyaml = self.findfile('hostpath_files/hostpath-snapshot.yaml')
        test_snap_name = 'hostpath-test-snapshot'
        # create StorageClass
        self.kubectl('create -f ' + classyaml)
        # create PV
        self.kubectl('create -f ' + pvyaml)
        # create PVC
        self.kubectl('create -f ' + pvcyaml)

        # get snapshots and snapshot data, count them
        rv, out = self.kubectl('get volumesnapshot -o json')
        json_out = json.loads(out)
        snap_num_pre = len(json_out['items'])

        rv, out = self.kubectl('get volumesnapshotdata -o json')
        json_out = json.loads(out)
        snap_data_num_pre = len(json_out['items'])
        if snap_data_num_pre != 0:
            print("*******")
            print(out)
            print("*******")

        # snapshot PVC
        self.kubectl('create -f ' + snapyaml)
        time.sleep(3)

        rv, out = self.kubectl('get volumesnapshot -o json')
        snapshot_list = json.loads(out)
        snap_num = len(snapshot_list['items'])
        self.assertEqual(snap_num, snap_num_pre + 1)

        # this may take time... try several times before giving up
        for i in range(1, 10):
            time.sleep(3)
            rv, out = self.kubectl('get volumesnapshotdata -o json')
            snapshot_data_list = json.loads(out)
            snap_data_num = len(snapshot_data_list['items'])
            if snap_data_num != snap_data_num_pre:
                break
        # this is super odd: dump the output
        if snap_data_num != (snap_data_num_pre + 1):
            print("*******")
            print(out)
            print("*******")

        self.assertEqual(snap_data_num, snap_data_num_pre + 1)

        # find the created snapshot in the list
        created_snapshot = None
        for snap in snapshot_list['items']:
            if snap['metadata']['name'] == test_snap_name:
                created_snapshot = snap
                break
        self.assertIsNotNone(created_snapshot)
        # verify kubectl can find it
        self.kubectl('get volumesnapshot ' + test_snap_name)

        # find the corresponding snapshotdata
        created_snapshot_data = None
        for snap_data in snapshot_data_list['items']:
            if snap_data['spec']['volumeSnapshotRef']['name'] == 'default/' + test_snap_name:
                created_snapshot_data = snap_data
                break
        self.assertIsNotNone(created_snapshot_data)
        # verify the snapshot really exists
        snap_source = created_snapshot_data['spec']['hostPath']['snapshot']
        self.assertIsNotNone(snap_source)
        self.assertTrue(os.path.isfile(snap_source))

    def test_20_delete(self):
        """
        Test deletion of a PVC snapshot
        """
        test_snap_name = 'hostpath-test-snapshot'
        # get snapshots and snapshot data, count them
        rv, out = self.kubectl('get volumesnapshot -o json')
        json_out = json.loads(out)
        snap_num_pre = len(json_out['items'])

        rv, out = self.kubectl('get volumesnapshotdata -o json')
        json_out = json.loads(out)
        snap_data_num_pre = len(json_out['items'])

        # remove snapshot
        self.kubectl('delete volumesnapshot ' + test_snap_name)
        time.sleep(1)

        rv, out = self.kubectl('get volumesnapshot -o json')
        snapshot_list = json.loads(out)
        snap_num = len(snapshot_list['items'])
        self.assertEqual(snap_num, snap_num_pre - 1)

        rv, out = self.kubectl('get volumesnapshotdata -o json')
        snapshot_data_list = json.loads(out)
        snap_data_num = len(snapshot_data_list['items'])
        self.assertEqual(snap_data_num, snap_data_num_pre - 1)

        # find the created snapshot in the list
        created_snapshot = None
        for snap in snapshot_list['items']:
            if snap['metadata']['name'] == test_snap_name:
                created_snapshot = snap
                break
        self.assertIsNone(created_snapshot)
        # verify kubectl can't find it
        rv, out = self.kubectl('get volumesnapshot ' + test_snap_name, ignore_error=True)
        self.assertNotEqual(rv, 0)

        # find the corresponding snapshotdata
        created_snapshot_data = None
        for snap_data in snapshot_data_list['items']:
            if snap_data['spec']['volumeSnapshotRef']['name'] == 'default/' + test_snap_name:
                created_snapshot_data = snap_data
                break
        # none should be found
        self.assertIsNone(created_snapshot_data)


