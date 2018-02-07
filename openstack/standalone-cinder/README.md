## Standalone-cinder external provisioner
The standalone-cinder external provisioner fulfills persistent
volume claims by creating cinder volumes and mapping them
to natively supported volume sources.  This approach allows
cinder to be used as a storage service whether or not the
cluster is deployed on Openstack.  By mapping cinder
volume connection information to native volume sources,
persistent volumes can be attached and mounted using the
standard kubelet machinery and without help from cinder.

### Mapping cinder volumes to native PVs
The provisioner works by mapping cinder volume connections 
(iscsi, rbd, fc, etc) to the corresponding native/raw kubernetes
volume types.  New cinder types can be supported in the provisioner
by creating a new implementation of the volunmeMapper interface.  The
implementation is responsible for building a PersistentVolumeSource
from cinder connection information and for setup and teardown of any
authentication (CHAP secret, cephx secret, etc) if required.

Support for cinder backends without a corresponding kubernetes raw
volume implementation could be added in the future by providing a
FlexVolume implementation for the type.

### Connecting to cinder
The provisioner directly uses the gophercloud SDK to connect to
cinder (as opposed to use of the cloudprovider interface).  The
intention is to support two modes of operation: a conventional
cinder deployment with keystone managing authentication and
providing the service catalog, and a standalone configuration where
cinder is accessed directly.

Cinder deployments can be used by supplying a clound
config file identical to the one you would use to configure an
openstack cloud provider or by specifying authentication parameters
in the environment.

```ini
# Example configuration: keystone auth via cloudconfig
[Global]
auth-url=http://keystone-host:5000/v2.0
username=admin
password=Passw0rd!
region=RegionOne
tenant-id=637b7373213d439d8119285244481456
```

```ini
# Example configuration: noauth via cloudconfig
[Global]
cinder-endpoint=http://cinder-host:8776/v2
username=admin
tenant-name=admin
```

```sh
# Example configuration: keystone auth via environment variables
OS_AUTH_URL=http://keystone-host:5000/v2.0
OS_USERNAME=admin
OS_PASSWORD=Passw0rd!
OS_TENANT_ID=637b7373213d439d8119285244481456
OS_REGION_NAME=RegionOne
```

```sh
# Example configuration: noauth via environment variables
OS_CINDER_ENDPOINT=http://cinder-host:8776/v2
OS_USERNAME=admin
OS_TENANT_NAME=admin
```

### Workflows
| User       | Kubernetes   | Provisioner  | Cinder       |
| ---------- | ------------ | ------------ | ------------ |
| **Provision storage** | | | |
| Issue a PVC for a 100GB RWO volume | | | |
| | Posted the PVC on API server | | |
| | | Acquire the PVC and call cinder create | |
| | | | Create 100GB volume and return info |
| | | Call cinder reserve | |
| | | | Volume is marked as reserved/attaching |
| | | Call cinder os-initialize_connection | |
| | | | Create connection on storage server and return info (ISCSI target and LUN info) |
| | | Create PV using ISCSI as raw volume type using connection info ||
| | Bind the PV to the PVC | | |
| **Use storage** | | | |
| Create a Pod including the PVC | | | |
| | Attach ISCSI PV to node | | |
| | Create filesystem on device and mount into Pod | | |
| | Pod is running | | |
| Delete Pod | | | |
| | Pod is stopped | | |
| | Unmount filesystem | | |
| | Detach ISCSI PV | | |
| **Delete storage** | | | |
| Delete PVC | | | |
| | | Call cinder os-terminate_connection | |
| | | | Remove connection on storage server |
| | | Call cinder unreserve | |
| | | | Volume is marked as available |
| | | Call cinder delete | |
| | | | Delete volume from storage |
| | Delete PV | | |
