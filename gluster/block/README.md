# Glusterblock Volume Provisioner for Kubernetes 1.5+


[![Docker Repository on Quay](https://quay.io/repository/external_storage/glusterblock-provisioner/status "Docker Repository on Quay")](https://quay.io/repository/external_storage/glusterblock-provisioner)
```
quay.io/external_storage/glusterblock-provisioner:latest
```


## What is Gluster Block Provisioner ?

Gluster Block Provisioner is an external provisioner which dynamically provision gluster block volumes ( ISCSI volumes ) on demand. The persistentVolumeClaim which has been requested with this external provisioner's identity ( for eg# `gluster.org/glusterblock`)  will be served by this provisioner. This provisioner is capable of operating on couple of modes ( `gluster-block` and `heketi`).

`gluster-block` mode :  This is an experimental or test mode on which the provisioner will directly talk to `gluster-block` utility or command line interface of gluster-block. 

`heketi` mode : This is the recommended/supported mode on which the provisioner will talk to `heketi's` Block API to provision gluster block volumes.  

This project is related to and relies on the following projects:

* [gluster-block](https://github.com/gluster/gluster-block)
* [heketi](https://github.com/heketi/heketi) - GlusterFS volume management REST API
* [gluster-kubernetes](https://github.com/gluster/gluster-kubernetes)- Kubernetes integrations for GlusterFS


## Build Gluster Block Provisioner and container image

If you want to build the container from source instead of pulling the docker image, simply run the following from the `glusterfs/block/` directory:

 Step 1: Build the provisioner binary
```
[root@localhost]# go build glusterblock-provisioner.go
```

Step 2:  Get Gluster Block Provisioner Container image
```
[root@localhost]# docker pull quay.io/external_storage/glusterblock-provisioner:latest
```

## Start Kubernetes Cluster

The following steps assume you have a Kubernetes cluster up and running

## Start glusterblock provisioner

The following example uses `gluster.org/glusterblock` as the identity for the instance and assumes kubeconfig is at `/root/.kube`. The identity should remain the same if the provisioner restarts. If there are multiple provisioners, each should have a different identity.

```
[root@localhost] docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host  glusterblock-provisioner  -master=http://127.0.0.1:8080 -kubeconfig=/kube/config -id=gluster.org/glusterblock
```


## Create a glusterblock Storage Class

```
[root@localhost] kubectl create -f glusterblock-class.yaml
```

The available storage class parameter are listed below:

```yaml

parameters:
    resturl: "http://127.0.0.1:8081"
    restuser: "admin"
    restsecretnamespace: "default"
    restsecretname: "heketi-secret"
    hacount: "3"
    chapauthenabled: "true"
    opmode: "gluster-block"
    blockmodeargs: "glustervol=blockmaster1,hosts=10.67.116.108"

```


### Global parameters applicable for both modes:

* `opmode`: This value decide in which mode gluster block provisioner has to work and the default is `heketi`. This is an optional parameter.

* `chapauthenabled`: This value has to be set to `false` if you want to provision block volume without CHAP authentication. This is an optional parameter defaults to `true`. 

* `hacount`: This is the count of number of paths to the block target server. This provide high availability via multipathing capability of iscsi. If there is a path failure, the I/Os will not be disturbed and will be served via another available paths.

* `volumenameprefix` : By default dynamically provisioned volumes has the naming schema of vol_UUID format. With this option present in storageclass, an admin can now prefix the desired volume name from storageclass. If volumenameprefix storageclass parameter is set, the dynamically provisioned volumes are created in below format where _ is the field separator/delimiter:

`volumenameprefix_Namespace_PVCname_randomUUID`

Please note that, the value for this parameter cannot contain _ in storageclass. This is an optional parameter.

### Heketi Mode Parameters:

If provisioner want to operate on `heketi` mode, below args can be  filled in storageclass accordingly.

* `resturl` : Gluster REST service/Heketi service url which provision gluster block volumes on demand. The general format should be `IPaddress:Port` and this is a mandatory parameter for GlusterFS dynamic provisioner. If Heketi service is exposed as a routable service in openshift/kubernetes setup, this can have a format similar to
`http://heketi-storage-project.cloudapps.mystorage.com` where the fqdn is a resolvable heketi service url.

* `restuser` : Gluster REST service/Heketi user who has access to create volumes in the Gluster Trusted Pool.

* `restsecretnamespace` + `restsecretname` : Namespace and Name of the Secret instance that contains user password to use when talking to heketi. These parameters are optional, An empty password will be used when both `restsecretnamespace` and `restsecretname` are omitted. The provided secret must have type matching your provisioner ID (e.g. `gluster.org/glusterblock`).


### Gluster-Block Mode parameters:

If provisioner want to operate on `gluster-block`, below args are required to be filled in storageclass.

* `blockmodeargs`:

This mode requires `glustervol` name and `hosts` to be mentioned in `,` seperated values as shown below. This is a mandatory parameter to be filled
in storage class parameter.

```
"glustervol=blockmaster1,hosts=10.67.116.108"
```

##  Testing: Create a PersistentVolumeClaim

```
[root@localhost]# kubectl create -f glusterblock-claim1.yaml
persistentvolumeclaim "claim1" created

[root@localhost]# kubectl get pvc
NAME      STATUS    VOLUME                                     CAPACITY   ACCESSMODES   STORAGECLASS   AGE
claim1    Bound     pvc-b7045edf-3a26-11e7-af53-c85b7636c232   1Gi        RWX           glusterblock   56s
[root@localhost]# kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM            STORAGECLASS   REASON    AGE
pvc-b7045edf-3a26-11e7-af53-c85b7636c232   1Gi        RWX           Delete          Bound     default/claim1   glusterblock             46s

[root@localhost]# kubectl get pvc,pv
NAME         STATUS    VOLUME                                     CAPACITY   ACCESSMODES   STORAGECLASS   AGE
pvc/claim1   Bound     pvc-b7045edf-3a26-11e7-af53-c85b7636c232   1Gi        RWX           glusterblock   1m

NAME                                          CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM            STORAGECLASS   REASON    AGE
pv/pvc-b7045edf-3a26-11e7-af53-c85b7636c232   1Gi        RWX           Delete          Bound     default/claim1   glusterblock             1m
```

