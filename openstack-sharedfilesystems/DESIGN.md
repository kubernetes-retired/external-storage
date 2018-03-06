# OpenStack Manila External Provisioner


## Deployment Strategy into OpenShift
Similar to other external provisioners, i.e. Manila external provisioner will be distributed in a Docker image and deployed manually in a pod into OpenShift.


## Security
Only supplemental group will be used.

### Supplemental Group
The [gidallocator package](https://github.com/wongma7/efs-provisioner/blob/master/pkg/gidallocator/allocator.go) will be used to allocate a GID for each provisioned share. The GID is given as a supplemental group to the process(es) running in a pod that mounted the provisioned share.
In addition, Manila access control for the provisioned share will be set to `ip 0.0.0.0` immediately after creation so that according to the Manila documentation the share can be mounted from any machine.

### Supplemental Group and Manila Access Control Feature
In addition, the below approach is left for a potential future improvement:
- Use Supplemental group as described in the Supplemental group section.
- At the time of provisioning generate a certificate and access control to the provisioned share will be allowed only using the certificate.
- Implement a new (Flex Volume or out-of-tree or in-tree) plugin for mounting Manila share using the generated certificate.


## `gophercloud` Library
The [`gophercloud` library](https://github.com/gophercloud/gophercloud) will be used for communication with Manila API.

### Authentication to Manila Service
`gophercloud`library reads values for authentication from environment variables. The below environment variable combinations were successfully used for authentication against Keystone version Newton `2:10.0.0-0ubuntu1`:

```
OS_USERNAME=demo
OS_PASSWORD=openstack
OS_AUTH_URL=http://localhost:35357/v3
OS_DOMAIN_NAME=Default
OS_TENANT_NAME=demo
```

```
OS_USERID=7e22ce01934c47dcae0f90e96cdfcf03
OS_PASSWORD=openstack
OS_AUTH_URL=http://localhost:35357/v3
OS_TENANT_ID=ecbc0da9369f41e3a8a17e49a425ff2d
```

```
OS_USERNAME=demo
OS_PASSWORD=openstack
OS_AUTH_URL=http://localhost:35357/v3
OS_DOMAIN_ID=default
OS_TENANT_NAME=demo
```

```
OS_USERNAME=demo
OS_PASSWORD=openstack
OS_AUTH_URL=http://localhost:35357/v3
OS_DOMAIN_ID=default
OS_TENANT_ID=ecbc0da9369f41e3a8a17e49a425ff2d
```

Note: the `OS_REGION_NAME` environment variable shall be read by the provisioner and shall be used for selecting a Manila service endpoint. The `gophercloud` library doesn't support the `OS_REGION_NAME` environment variable and it will take very significant effort to introduce this env. variable into the library.

#### Authentication Token Limited Validity
Authentication token created during authentication and used in follow-up API calls has limited validity and should expire after 1 hour. That's why the provisioner shall authenticate every time just before it is going to either provision or delete a share.

### Share Creation and Deletion
[`Create`, `Delete` and `Get` methods](https://github.com/gophercloud/gophercloud/blob/master/openstack/sharedfilesystems/v2/shares/requests.go) are already available and will be needed to create a new share or delete an existing share.

Important: the deletion shall be implemented in such a way that even though the share still contains some data the share is deleted. This behaviour may depend on the used Manila back-end.

### Access Control
Access control must be set to every newly created share, otherwise the share can't be mounted.

The necessary API calls are already implemented in the gophercloud library.


## Testing
[Testing pyramid](https://testing.googleblog.com/2015/04/just-say-no-to-more-end-to-end-tests.html) will be followed, however, end-to-end tests will be skipped because of lack of OpenStack Manila test environment that will be capable of running OpenShift and provisioning NFS shares.

### Unit tests
Unit tests that will be result of test driven development.

### Integration tests
No integration tests are planned.

### Kubernetes E2E tests
It is not possible to add automated E2E tests into Kubernetes CI.

As there is no OpenStack environment with Manila service that can provision NFS share types Kubernetes E2E tests will not be performed.

### OpenShift E2E tests
OpenStack environment with Manila service that can provision NFS share types is not available so E2E tests will not be performed.

Currently, it is not possible to add automated tests of the provisioner to OpenShift CI.


## A Share Creation
Share creation consists of:
- [Create request](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#create-share) that either fails or results in a share being in state `creating`.
- `created` state waiting loop: because a successful share create request results in a `creating` share it is necessary to wait for a share to be created afterwards. So a waiting loop that periodically [shows the share status](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#show-share-details) after 1, 2, 4, 8, etc. seconds and waits until the status changes to `created` or the waiting timeouts (configurable timeout; default 180 seconds).
- Access control settings will be set to `ip 0.0.0.0` for every newly created share.


## Storage Class Example(s)
```
apiVersion: storage.k8s.io/v1beta1
kind: StorageClass
metadata:
  name: manilaNFSshare
provisioner: kubernetes.io/manila
parameters:
  type: default
  zones: nova1, nova2, nova3
```
Optional parameter(s):
- `type` share type configured by administrator of Manila service.
- `zones` a set of zones; one of the zones will be used as the `availability_zone` in the [Create request](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#create-share). In case the `zones` parameter is not specified the `availability_zone` in the [Create request](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#create-share) is filled with default zone `nova`.

Unavailable parameter(s):
- `share_proto` that is a mandatory parameter in the [Create request](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#create-share). The value of `NFS` will be always used.

[Create request](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#create-share) optional parameters that won't be supported in Storage Class:
- `volume_type`

**IMPORTANT**: OpenStack Nova availability zones must match Manila availability 1:1, otherwise a share may be provisioned in an availability zone where the Kubernetes cluster doesn't have a node.

**IMPORTANT**: [Generic Storage Topology solution that works well with CSI and Local Storage](https://docs.google.com/spreadsheets/d/1t4z5DYKjX2ZDlkTpCnp18icRAQqOE85C1T1r2gqJVck/edit#gid=1993207982) design is planned for Kubernetes 1.9. Manila external provisioner should be re-designed according to the Generic Storage Topology design.

## PVC Example(s)
```
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: tinyshare
  annotations:
    "volume.beta.kubernetes.io/storage-class": "manilaNFSshare"
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2G
```
Mandatory parameter(s):
- `storage` and the requested storage size must be whole integer number in GBs. In case it it not it is rounded to the closest higher value in GBs.

Ignored parameter(s):
- `accessModes` are ignored. A PV created on a PVC demand will contain all access modes supported by the corresponding filesystem specified in the corresponding Storage Class (note: currently, only NFS is supported that's why all ReadWriteOnce, ReadOnlyMany and ReadWriteMany access modes are filled into the PV).

[Create request](http://developer.openstack.org/api-ref/shared-file-systems/?expanded=create-share-detail#create-share) optional parameters that won't be supported in PVC:
- `name`
- `description`
- `display_name`
- `display_description`
- `snapshot_id`
- `is_public`
- `metadata`
- `share_network_id`
- `consistency_group_id`

## A Share Deletion
The `func Delete()` always authenticates to the Manila API using environment variables like `OS_TENANT_ID`, etc. In case the `OS_TENANT_ID` is different than `OS_TENANT_ID` used to provision the share, the share cannot be deleted by the provisioner. In such case shares must be deleted manually.
