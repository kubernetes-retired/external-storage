# glusterblock Volume Provisioner for Kubernetes 1.5+


[![Docker Repository on Quay](https://quay.io/repository/external_storage/glusterblock-provisioner/status "Docker Repository on Quay")](https://quay.io/repository/external_storage/glusterblock-provisioner)
```
quay.io/external_storage/glusterblock-provisioner:latest
```

# Test instruction

## Build glusterblock-provisioner and container image

```bash
[root@localhost]# go build glusterblock-provisioner.go
[root@localhost]# docker build -t glusterblock-provisioner .
```

## Start Kubernetes local cluster

## Start glusterblock provisioner

The following example uses `glusterblock-provisioner-1` as the identity for the instance and assumes kubeconfig is at `/root/.kube`. The identity should remain the same if the provisioner restarts. If there are multiple provisioners, each should have a different identity.

```bash
docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host  glusterblock-provisioner /usr/local/bin/glusterblock-provisioner -master=http://127.0.0.1:8080 -kubeconfig=/kube/config -id=glusterblock-provisioner-1
```

## Create a glusterblock Storage Class

```bash
kubectl create -f glusterblock-class.yaml
```

The available storage class parameter are listed below:

```yaml

parameters:
    resturl: "http://127.0.0.1:8081"
    restuser: "admin"
    secretnamespace: "default"
    secretname: "heketi-secret"
    opmode: "gluster-block:glustervol=blockmaster1,hosts=10.67.116.108"
    hacount: "3"




```

* `resturl` : Gluster REST service/Heketi service url which provision gluster block volumes on demand. The general format should be `IPaddress:Port` and this is a mandatory parameter for GlusterFS dynamic provisioner. If Heketi service is exposed as a routable service in openshift/kubernetes setup, this can have a format similar to
`http://heketi-storage-project.cloudapps.mystorage.com` where the fqdn is a resolvable heketi service url.

* `restuser` : Gluster REST service/Heketi user who has access to create volumes in the Gluster Trusted Pool.

* `secretNamespace` + `secretName` : Identification of Secret instance that contains user password to use when talking to Gluster REST service. These parameters are optional, empty password will be used when both `secretNamespace` and `secretName` are omitted. The provided secret must have type "gluster.org/glusterblock".

* `opmode`: Gluster Block provisioner can operate in more than one mode for provisioning gluster block volume. Available modes are `gluster-block` and `heketi`. Heketi will be the default or recommended operation mode.

* `hacount`: This is the count of number of paths to the block target server. This provide high availability via multipathing capability of iscsi. If there is a path failure, the I/Os will not be disturbed and will be served via another available paths.

## Create a claim

```bash
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
