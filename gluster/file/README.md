# glusterfile Volume Provisioner for Kubernetes 1.5+

[![Docker Repository on Quay](https://quay.io/repository/external_storage/glusterfile-provisioner/status "Docker Repository on Quay")](https://quay.io/repository/external_storage/glusterfile-provisioner)
```
quay.io/external_storage/glusterfile-provisioner:latest
```
## What is Gluster File Provisioner ?

Gluster File Provisioner is an external provisioner which dynamically provisions gluster file volumes  on demand. The persistentVolumeClaim which has been requested with this external provisioner's identity (for e.g.# `gluster.org/glusterfile`)  will be served by this provisioner.

This project is related to and relies on the following projects:

* [glusterfs](https://github.com/gluster/glusterfs)
* [heketi](https://github.com/heketi/heketi)
* [gluster-kubernetes](https://github.com/gluster/gluster-kubernetes)

## Build Gluster File Provisioner and container image

If you want to build the container from source instead of pulling the docker image, please follow below steps:

 Step 1: Build the provisioner binary
```
[root@localhost]# go build glusterfile-provisioner.go
```

Step 2:  Get Gluster File Provisioner Container image
```
[root@localhost]# docker pull quay.io/external_storage/glusterfile-provisioner:latest
```

## Start Kubernetes Cluster
The following steps assume you have a Kubernetes cluster up and running

## Start glusterfile provisioner

The following example uses `gluster.org/glusterfile` as the identity for the instance and assumes kubeconfig is at `/root/.kube`. The identity should remain the same if the provisioner restarts. If there are multiple provisioners, each should have a different identity.

```
[root@localhost] docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host  glusterfile-provisioner  -master=http://127.0.0.1:8080 -kubeconfig=/kube/config -id=gluster.org/glusterfile
```

## Create a glusterfile Storage Class

```
[root@localhost] kubectl create -f examples/class.yaml
```

The available storage class parameter are listed below:

```yaml
parameters:
    resturl: "http://127.0.0.1:8081"
    restuser: "admin"
    restsecretnamespace: "default"
    restsecretname: "heketi-secret"
    clusterid: "454811fcedbec6316bc10e591a57b472"
    volumetype: "replicate:3"
    volumeoptions: "features.shard enable"
    volumenameprefix: "dept-dev"
    smartclone: "true"
    snapfactor: "10"
```

* `resturl` : Gluster REST service/Heketi service url which provision gluster File volumes on demand. The general format should be `IPaddress:Port` and this is a mandatory parameter for glusterfile dynamic provisioner. If Heketi service is exposed as a routable service in openshift/kubernetes setup, this can have a format similar to
`http://heketi-storage-project.cloudapps.mystorage.com` where the fqdn is a resolvable heketi service url.

* `restuser` : Gluster REST service/Heketi user who has access to create volumes in the Gluster Trusted Pool.

* `restsecretnamespace` + `restsecretname` : Identification of Secret instance that contains user password to use when talking to Gluster REST service. These parameters are optional, empty password will be used when both `restsecretnamespace` and `restsecretname` are omitted. The provided secret must have type "gluster.org/glusterfile".

* `gidMin` + `gidMax` : The minimum and maximum value of GID range for the storage class. A unique value (GID) in this range ( gidMin-gidMax ) will be used for dynamically provisioned volumes. These are optional values. If not specified, the volume will be provisioned with a value between 2000-2147483647 which are defaults for gidMin and gidMax respectively.

* `clusterid`: It is the ID of the cluster which will be used by Heketi when provisioning the volume. It can also be a list of comma separated cluster IDs. This is an optional parameter.

Note
To get the cluster ID, execute the following command:
~~~
# heketi-cli cluster list
~~~
* `volumetype` : The volume type and its parameters can be configured with this optional value. If the volume type is not mentioned, it's up to the provisioner to decide the volume type.
For example:

  'Replica volume':
    `volumetype: replicate:3` where '3' is replica count.
  'Disperse/EC volume':
    `volumetype: disperse:4:2` where '4' is data and '2' is the redundancy count.
  'Distribute volume':
    `volumetype: none`

For available volume types and its administration options refer: ([Administration Guide](http://docs.gluster.org/en/latest/Administrator%20Guide/Setting%20Up%20Volumes/))

* `volumeoptions` : This option allows to specify the gluster volume option which has to be set on the dynamically provisioned GlusterFS volume. The value string should be comma separated strings which need to be set on the volume. As shown in example, if you want to enable encryption on gluster dynamically provisioned volumes you can pass `client.ssl on, server.ssl on` options. This is an optional parameter.

For available volume options and its administration refer: ([Administration Guide](http://docs.gluster.org/en/latest/Administrator%20Guide/Managing%20Volumes/))

* `volumenameprefix` : By default dynamically provisioned volumes has the naming schema of `vol_UUID` format. With this option present in storageclass, an admin can now prefix the desired volume name from storageclass. If `volumenameprefix` storageclass parameter is set, the dynamically provisioned volumes are created in below format where `_` is the field separator/delimiter:

`volumenameprefix_Namespace_PVCname_randomUUID`

Please note that, the value for this parameter cannot contain `_` in storageclass. This is an optional parameter.

* `cloneenabled` : This option allows to create clone of PVCs if pvc is annotated with `k8s.io/CloneRequest`. The new PVC will be clone of pvc specified as the field value of `k8s.io/CloneRequest` annotation. This is an optional parameter and by default
this option is false/disabled.

* `snapfactor`: Dynamically provisioned volume's thinpool size can be configured with this parameter. The value for the parameter should be in range of 1-100, this value will be taken into account while creating thinpool for the provisioned volume. This is an optional parameter with default value of 1.

Additional Reference:

([How to configure Gluster on Kubernetes](https://github.com/gluster/gluster-kubernetes/blob/master/docs/setup-guide.md))

([How to configure Heketi](https://github.com/heketi/heketi/wiki/Setting-up-the-topology))

When the persistent volumes are dynamically provisioned, the Gluster plugin automatically create an endpoint and a headless service in the name `glusterfile-dynamic-<claimname>`. This dynamic endpoint and service will be deleted automatically when the persistent volume claim is deleted.


## Testing: Create a PersistentVolumeClaim

```
[root@localhost]# kubectl create -f examples/claim1.yaml
persistentvolumeclaim "claim1" created

[root@localhost]# kubectl get pvc
NAME      STATUS    VOLUME                                     CAPACITY   ACCESSMODES   STORAGECLASS   AGE
claim1    Bound     pvc-b28a67ff-0fce-11e8-a7cb-c85b7636c232   1Gi        RWX           glusterfile   56s
[root@localhost]# kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM            STORAGECLASS   REASON    AGE
pvc-b28a67ff-0fce-11e8-a7cb-c85b7636c232   1Gi        RWX           Delete          Bound     default/claim1   glusterfile             46s

[root@localhost]# kubectl get pvc,pv
NAME         STATUS    VOLUME                                     CAPACITY   ACCESSMODES   STORAGECLASS   AGE
pvc/claim1   Bound     pvc-b28a67ff-0fce-11e8-a7cb-c85b7636c232   1Gi        RWX           glusterfile   1m

NAME                                          CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM            STORAGECLASS   REASON    AGE
pv/pvc-b28a67ff-0fce-11e8-a7cb-c85b7636c232   1Gi        RWX           Delete          Bound     default/claim1   glusterfile             1m
```