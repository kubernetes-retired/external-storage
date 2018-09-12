# Deployment

## Getting the provisioner image
To get the Docker image onto the machine where you want to run nfs-provisioner, you can either build it or pull the newest release from Quay. You may use the unstable `latest` tag if you wish, but all the example yamls reference the newest versioned release tag.

### Building
Building the project will only work if the project is in your `GOPATH`. Download the project into your `GOPATH` directory by using `go get` or cloning it manually.

```
$ go get github.com/kubernetes-incubator/external-storage
```

Now build the project and the Docker image by checking out the latest release and running `make container` in the project directory.

```
$ cd $GOPATH/src/github.com/kubernetes-incubator/external-storage/nfs
$ make container
```

### Pulling

If you are running in Kubernetes, it will pull the image from Quay for you. Or you can do it yourself.

```
$ docker pull quay.io/kubernetes_incubator/nfs-provisioner:latest
```

## Deploying the provisioner
Now the Docker image is on your machine. Bring up a 1.4+ cluster if you don't have one up already.

```
$ ALLOW_SECURITY_CONTEXT=true API_HOST_IP=0.0.0.0 $GOPATH/src/k8s.io/kubernetes/hack/local-up-cluster.sh
```

Decide on a unique name to give the provisioner that follows the naming scheme `<vendor name>/<provisioner name>` where `<vendor name>` cannot be "kubernetes.io." The provisioner will only provision volumes for claims that request a `StorageClass` with a `provisioner` field set equal to this name. For example, the names of the in-tree GCE and AWS provisioners are `kubernetes.io/gce-pd` and `kubernetes.io/aws-ebs`.

Decide how to run nfs-provisioner and follow one of the below sections. The recommended way is running it as a [single-instance stateful app](http://kubernetes.io/docs/tutorials/stateful-application/run-stateful-application/), where you create a `Deployment`/`StatefulSet` and back it with some persistent storage like a `hostPath` volume. Running outside of Kubernetes as a standalone container or binary is for when you want greater control over the app's lifecycle and/or the ability to set per-PV quotas.

* [In Kubernetes - Deployment](#in-kubernetes---deployment-of-1-replica)
* [In Kubernetes - StatefulSet](#in-kubernetes---statefulset-of-1-replica)
* [Outside of Kubernetes - container](#outside-of-kubernetes---container)
* [Outside of Kubernetes - binary](#outside-of-kubernetes---binary)

### In Kubernetes - Deployment of 1 replica

Edit the `provisioner` argument in the `args` field in `deploy/kubernetes/deployment.yaml` to be the provisioner's name you decided on.

`deploy/kubernetes/deployment.yaml` specifies a `hostPath` volume `/srv` mounted at `/export`. The `/export` directory is where the provisioner stores its state and provisioned `PersistentVolumes'` data, so by mounting a volume there, you specify it as the backing storage for provisioned PVs. You may edit the `hostPath` or even mount some other type of volume at `/export`, like a `PersistentVolumeClaim`. Note that the volume mounted there must have a [supported file system](https://github.com/nfs-ganesha/nfs-ganesha/wiki/Fsalsupport#vfs) on it: any local filesystem on Linux is supported & NFS is not supported.

Note that if you continue with the `hostPath` volume, its path must exist on the node the provisioner is scheduled to, so you may want to use a `nodeSelector` to choose a particular node and ensure the directory exists there: `mkdir -p /srv`. If SELinux is enforcing on the node, you may need to make the container [privileged](http://kubernetes.io/docs/user-guide/security-context/) or change the security context of the directory on the node: `sudo chcon -Rt svirt_sandbox_file_t /srv`.

`deploy/kubernetes/deployment.yaml` also configures a service. The deployment's pod will use the service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP, because the service's name is passed in via the `SERVICE_NAME` env variable.

Create the deployment and its service.

```
$ kubectl create -f deploy/kubernetes/psp.yaml # or if openshift: oc create -f deploy/kubernetes/scc.yaml\
# Set the subject of the RBAC objects to the current namespace where the provisioner is being deployed
$ NAMESPACE=`kubectl config get-contexts | grep '^*' | tr -s ' ' | cut -d' ' -f5`
$ sed -i'' "s/namespace:.*/namespace: $NAMESPACE/g" ./deploy/kubernetes/rbac.yaml
$ kubectl create -f deploy/kubernetes/rbac.yaml
$ kubectl create -f deploy/kubernetes/deployment.yaml
```

### In Kubernetes - StatefulSet of 1 replica

The procedure for running a stateful set is identical to [that for a deployment, above,](#in-kubernetes---deployment-of-1-replica) so wherever you see `deployment` there, replace it with `statefulset`. The benefit is that you get a stable hostname. But note that stateful sets are in beta. Note that the service cannot be headless, unlike in most examples of stateful sets.

### Outside of Kubernetes - container

The container is going to need to run with one of `master` or `kubeconfig` set. For the `kubeconfig` argument to work, the config file, and any certificate files it references by path like `certificate-authority: /var/run/kubernetes/apiserver.crt`, need to be inside the container somehow. This can be done by creating Docker volumes, or copying the files into the folder where the Dockerfile is and adding lines like `COPY config /.kube/config` to the Dockerfile before building the image. 

Run nfs-provisioner with `provisioner` equal to the name you decided on, and one of `master` or `kubeconfig` set. It needs to be run with capability `DAC_READ_SEARCH` in order for Ganesha to work. Optionally, it should be run also with capability `SYS_RESOURCE` so that it can set a higher limit for the number of opened files Ganesha may have. If you are using Docker 1.10 or newer, it also needs a more permissive seccomp profile: `unconfined` or `deploy/docker/nfs-provisioner-seccomp.json`.

You may want to specify the hostname the NFS server exports from, i.e. the server IP to put on PVs, by setting the `server-hostname` flag.

```
$ docker run --cap-add DAC_READ_SEARCH --cap-add SYS_RESOURCE \
--security-opt seccomp:deploy/docker/nfs-provisioner-seccomp.json \
-v $HOME/.kube:/.kube:Z \
quay.io/kubernetes_incubator/nfs-provisioner:latest \
-provisioner=example.com/nfs \
-kubeconfig=/.kube/config
```
or
```
$ docker run --cap-add DAC_READ_SEARCH --cap-add SYS_RESOURCE \
--security-opt seccomp:deploy/docker/nfs-provisioner-seccomp.json \
quay.io/kubernetes_incubator/nfs-provisioner:latest \
-provisioner=example.com/nfs \
-master=http://172.17.0.1:8080
```

You may want to create & mount a Docker volume at `/export` in the container. The `/export` directory is where the provisioner stores its provisioned `PersistentVolumes'` data, so by mounting a volume there, you specify it as the backing storage for provisioned PVs. The volume can then be reused by another container if the original container stops. Without Kubernetes you will have to manage the lifecycle yourself. You should give the container a stable IP somehow so that it can survive a restart to continue serving the shares in the volume.

You may also want to enable per-PV quota enforcement. It is based on xfs project level quotas and so requires that the volume mounted at `/export` be xfs mounted with the prjquota/pquota option. It also requires that it has the privilege to run `xfs_quota`.

With the two above options, the run command will look something like this.

```
$ docker run --privileged \
-v $HOME/.kube:/.kube:Z \
-v /xfs:/export:Z \
quay.io/kubernetes_incubator/nfs-provisioner:latest \
-provisioner=example.com/nfs \
-kubeconfig=/.kube/config \
-enable-xfs-quota=true
```

### Outside of Kubernetes - binary

Running nfs-provisioner in this way allows it to manipulate exports directly on the host machine. It will create & store all its data at `/export` so ensure the directory exists and is available for use. It runs assuming the host is already running either NFS Ganesha or a kernel NFS server, depending on how the `use-ganesha` flag is set. Use with caution.

Run nfs-provisioner with `provisioner` equal to the name you decided on, one of `master` or `kubeconfig` set, `run-server` set false, and `use-ganesha` set according to how the NFS server is running on the host. It probably needs to be run as root. 

You may want to specify the hostname the NFS server exports from, i.e. the server IP to put on PVs, by setting the `server-hostname` flag.

```
$ sudo ./nfs-provisioner -provisioner=example.com/nfs \
-kubeconfig=$HOME/.kube/config \
-run-server=false \
-use-ganesha=false
```
or
```
$ sudo ./nfs-provisioner -provisioner=example.com/nfs \
-master=http://0.0.0.0:8080 \
-run-server=false \
-use-ganesha=false
```

You may want to enable per-PV quota enforcement. It is based on xfs project level quotas and so requires that the volume mounted at `/export` be xfs mounted with the prjquota/pquota option. Add the `-enable-xfs-quota=true` argument to enable it.

```
$ sudo ./nfs-provisioner -provisioner=example.com/nfs \
-kubeconfig=$HOME/.kube/config \
-run-server=false \
-use-ganesha=false \
-enable-xfs-quota=true
```

---

Now that you have finished deploying the provisioner, go to [Usage](usage.md) for info on how to use it.

---

#### Arguments

* `provisioner` - Name of the provisioner. The provisioner will only provision volumes for claims that request a StorageClass with a provisioner field set equal to this name.
* `master` - Master URL to build a client config from. Either this or kubeconfig needs to be set if the provisioner is being run out of cluster.
* `kubeconfig` - Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.
* `run-server` - If the provisioner is responsible for running the NFS server, i.e. starting and stopping NFS Ganesha. Default true.
* `use-ganesha` - If the provisioner will create volumes using NFS Ganesha (D-Bus method calls) as opposed to using the kernel NFS server ('exportfs'). If run-server is true, this must be true. Default true.
* `grace-period` - NFS Ganesha grace period to use in seconds, from 0-180. If the server is not expected to survive restarts, i.e. it is running as a pod & its export directory is not persisted, this can be set to 0. Can only be set if both run-server and use-ganesha are true. Default 90.
* `enable-xfs-quota` - If the provisioner will set xfs quotas for each volume it provisions. Requires that the directory it creates volumes in ('/export') is xfs mounted with option prjquota/pquota, and that it has the privilege to run xfs_quota. Default false.
* `failed-retry-threshold` - If the number of retries on provisioning failure need to be limited to a set number of attempts. Default 10
* `server-hostname` - The hostname for the NFS server to export from. Only applicable when running out-of-cluster i.e. it can only be set if either master or kubeconfig are set. If unset, the first IP output by `hostname -i` is used.
