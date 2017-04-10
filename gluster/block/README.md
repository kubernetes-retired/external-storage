# glusterblock Volume Provisioner for Kubernetes 1.5+


# Test instruction

* Build glusterblock-provisioner and container image

```bash
go build glusterblock-provisioner.go
docker build -t glusterblock-provisioner .
```

* Start Kubernetes local cluster

* Start glusterblock provisioner

The following example uses `glusterblock-provisioner-1` as the identity for the instance and assumes kubeconfig is at `/root/.kube`. The identity should remain the same if the provisioner restarts. If there are multiple provisioners, each should have a different identity.

```bash
docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host  glusterblock-provisioner /usr/local/bin/glusterblock-provisioner -master=http://127.0.0.1:8080 -kubeconfig=/kube/config -id=glusterblock-provisioner-1
```

* Create a glusterblock Storage Class

```bash
kubectl create -f class.yaml
```

The available storage class parameter are listed below:

```yaml
parameters:
    resturl: "http://127.0.0.1:8081"
    restuser: "admin"
    secretnamespace: "default"
    secretname: "heketi-secret"
    opmode: "executable"
    execpath: "/tmp/iscsicreate"
    hacount: "3"


```

* `resturl` : Gluster REST service/Heketi service url which provision gluster block volumes on demand. The general format should be `IPaddress:Port` and this is a mandatory parameter for GlusterFS dynamic provisioner. If Heketi service is exposed as a routable service in openshift/kubernetes setup, this can have a format similar to
`http://heketi-storage-project.cloudapps.mystorage.com` where the fqdn is a resolvable heketi service url.
* `restuser` : Gluster REST service/Heketi user who has access to create volumes in the Gluster Trusted Pool.
* `secretNamespace` + `secretName` : Identification of Secret instance that contains user password to use when talking to Gluster REST service. These parameters are optional, empty password will be used when both `secretNamespace` and `secretName` are omitted. The provided secret must have type "gluster.org/glusterblock".
* `opmode`: Gluster Block provisioner can operate in more than one mode for provisioning gluster block volume. Heketi will be the default or recommended operation mode. If the block provisioner is operating on `executable` mode, you have to fill `execpath` for provisioner to execute the provided binary.
*`execpath`: The path to the executable which is capable of creating gluster block volume and setting required environment variables for the block volume. The must environment volumes are `TARGET` and `IQN`.
*`hacount`: This is the count of number of paths to the block target server. This provide high availability via multipathing capability of iscsi. If there is a path failure, the I/Os will not be disturbed and will be served via another available paths.

* Create a claim

```bash
kubectl create -f claim.yaml
```
