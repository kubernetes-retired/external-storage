#!/usr/bin/env python

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

import os
import rados
import getopt
import sys
import json

"""
CEPH_CLUSTER_NAME=test CEPH_MON=172.24.0.4 CEPH_AUTH_ID=admin CEPH_AUTH_KEY=AQCMpH9YM4Q1BhAAXGNQyyOne8ZsXqWGon/dIQ== cephfs_provisioner.py -n foo -u bar
"""
try:
    import ceph_volume_client
    ceph_module_found = True
except ImportError as e:
    ceph_volume_client = None
    ceph_module_found = False

VOlUME_GROUP="kubernetes"
CONF_PATH="/etc/ceph/"

class CephFSNativeDriver(object):
    """Driver for the Ceph Filesystem.

    This driver is 'native' in the sense that it exposes a CephFS filesystem
    for use directly by guests, with no intermediate layer like NFS.
    """

    def __init__(self, *args, **kwargs):
        self._volume_client = None


    def _create_conf(self, cluster_name, mons):
        """ Create conf using monitors 
        Create a minimal ceph conf with monitors and cephx
        """
        conf_path = CONF_PATH + cluster_name + ".conf"
        conf = open(conf_path, 'w')
        conf.write("[global]\n")
        conf.write("mon_host = " + mons + "\n")
        conf.write("auth_cluster_required = cephx\nauth_service_required = cephx\nauth_client_required = cephx\n")
        conf.close()
        return conf_path

    def _create_keyring(self, cluster_name, id, key):
        """ Create client keyring using id and key
        """
        keyring = open(CONF_PATH + cluster_name + "." + "client." + id + ".keyring", 'w')
        keyring.write("[client." + id + "]\n")
        keyring.write("key = " + key  + "\n")
        keyring.write("caps mds = \"allow *\"\n")
        keyring.write("caps mon = \"allow *\"\n")
        keyring.write("caps osd = \"allow *\"\n")
        keyring.close()

    @property
    def volume_client(self):
        if self._volume_client:
            return self._volume_client

        if not ceph_module_found:
            raise ValueError("Ceph client libraries not found.")

        try:
            cluster_name = os.environ["CEPH_CLUSTER_NAME"]
        except KeyError:
            cluster_name = "ceph"
        try:     
            mons = os.environ["CEPH_MON"]
        except KeyError:
            raise ValueError("Missing CEPH_MON env")
        try:
            auth_id = os.environ["CEPH_AUTH_ID"]
        except KeyError:
            raise ValueError("Missing CEPH_AUTH_ID")
        try: 
            auth_key = os.environ["CEPH_AUTH_KEY"]
        except:
            raise ValueError("Missing CEPH_AUTH_KEY")

        conf_path = self._create_conf(cluster_name, mons)
        self._create_keyring(cluster_name, auth_id, auth_key)

        self._volume_client = ceph_volume_client.CephFSVolumeClient(
            auth_id, conf_path, cluster_name)
        try:
            self._volume_client.connect(None)
        except Exception:
            self._volume_client = None
            raise

        return self._volume_client

    def _authorize_ceph(self, volume_path, auth_id, readonly):
        path = self._volume_client._get_path(volume_path)

        # First I need to work out what the data pool is for this share:
        # read the layout
        pool_name = self._volume_client._get_ancestor_xattr(path, "ceph.dir.layout.pool")
        namespace = self._volume_client.fs.getxattr(path, "ceph.dir.layout.pool_namespace")

        # Now construct auth capabilities that give the guest just enough
        # permissions to access the share
        client_entity = "client.{0}".format(auth_id)
        want_access_level = 'r' if readonly else 'rw'
        want_mds_cap = 'allow r,allow {0} path={1}'.format(want_access_level, path)
        want_osd_cap = 'allow {0} pool={1} namespace={2}'.format(
            want_access_level, pool_name, namespace)

        try:
            existing = self._volume_client._rados_command(
                'auth get',
                {
                    'entity': client_entity
                }
            )
            # FIXME: rados raising Error instead of ObjectNotFound in auth get failure
        except rados.Error:
            caps = self._volume_client._rados_command(
                'auth get-or-create',
                {
                    'entity': client_entity,
                    'caps': [
                        'mds', want_mds_cap,
                        'osd', want_osd_cap,
                        'mon', 'allow r']
                })
        else:
            # entity exists, update it
            cap = existing[0]

            # Construct auth caps that if present might conflict with the desired
            # auth caps.
            unwanted_access_level = 'r' if want_access_level is 'rw' else 'rw'
            unwanted_mds_cap = 'allow {0} path={1}'.format(unwanted_access_level, path)
            unwanted_osd_cap = 'allow {0} pool={1} namespace={2}'.format(
                unwanted_access_level, pool_name, namespace)

            def cap_update(orig, want, unwanted):
                # Updates the existing auth caps such that there is a single
                # occurrence of wanted auth caps and no occurrence of
                # conflicting auth caps.

                cap_tokens = set(orig.split(","))

                cap_tokens.discard(unwanted)
                cap_tokens.add(want)

                return ",".join(cap_tokens)

            osd_cap_str = cap_update(cap['caps'].get('osd', ""), want_osd_cap, unwanted_osd_cap)
            mds_cap_str = cap_update(cap['caps'].get('mds', ""), want_mds_cap, unwanted_mds_cap)

            caps = self._volume_client._rados_command(
                'auth caps',
                {
                    'entity': client_entity,
                    'caps': [
                        'mds', mds_cap_str,
                        'osd', osd_cap_str,
                        'mon', cap['caps'].get('mon')]
                })
            caps = self._volume_client._rados_command(
                'auth get',
                {
                    'entity': client_entity
                }
            )

        # Result expected like this:
        # [
        #     {
        #         "entity": "client.foobar",
        #         "key": "AQBY0\/pViX\/wBBAAUpPs9swy7rey1qPhzmDVGQ==",
        #         "caps": {
        #             "mds": "allow *",
        #             "mon": "allow *"
        #         }
        #     }
        # ]
        assert len(caps) == 1
        assert caps[0]['entity'] == client_entity
        return caps[0]


    def create_share(self, path, user_id, size=None):
        """Create a CephFS volume.
        """
        volume_path = ceph_volume_client.VolumePath(VOlUME_GROUP, path)

        # Create the CephFS volume
        volume = self.volume_client.create_volume(volume_path, size=size)

        # To mount this you need to know the mon IPs and the path to the volume
        mon_addrs = self.volume_client.get_mon_addrs()

        export_location = "{addrs}:{path}".format(
            addrs=",".join(mon_addrs),
            path=volume['mount_path'])

        """TODO
        restrict to user_id
        """
        auth_result = self._authorize_ceph(volume_path, user_id, False)
        ret = {
            'path': export_location,
            'user': auth_result['entity'],
            'auth': auth_result['key']
        }
        return json.dumps(ret)


    def delete_share(self, path, user_id):
        volume_path = ceph_volume_client.VolumePath(VOlUME_GROUP, path)
        self.volume_client._deauthorize(volume_path, user_id)
        self.volume_client.delete_volume(volume_path)
        self.volume_client.purge_volume(volume_path)

    def __del__(self):
        if self._volume_client:
            self._volume_client.disconnect()
            self._volume_client = None

def main():
    create = True
    share = ""
    user = ""
    cephfs = CephFSNativeDriver()
    try:
        opts, args = getopt.getopt(sys.argv[1:], "rn:u:", ["remove"])
    except getopt.GetoptError:
        print "Usage: " + sys.argv[0] + " --remove -n share_name -u ceph_user_id"
        sys.exit(1)

    for opt, arg in opts:
        if opt == '-n':
            share = arg
        elif opt == '-u':
            user = arg
        elif opt in ("-r", "--remove"):
            create = False

    if share == "" or user == "":
        print "Usage: " + sys.argv[0] + " --remove -n share_name -u ceph_user_id"
        sys.exit(1)

    if create == True:
        print cephfs.create_share(share, user)    
    else:
        cephfs.delete_share(share, user)    
        
        
if __name__ == "__main__":
    main()
