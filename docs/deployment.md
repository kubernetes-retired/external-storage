# Deployment

## Getting the provisioner image
To get the Docker image onto the machine where you want to run nfs-provisioner, you can either build it or pull the latest version from Docker Hub.

### Building
Building the project will only work if the project is in your `GOPATH`. Download the project into your `GOPATH` directory by using `go get` or cloning it manually.

```
$ go get github.com/kubernetes-incubator/nfs-provisioner
```

Now build the project and the Docker image by running `make container` in the project directory.

```
$ cd $GOPATH/src/github.com/kubernetes-incubator/nfs-provisioner
$ make container
```

### Pulling

If you are running in Kubernetes, it will pull the image from Docker Hub for you. Or you can do it yourself.

```
$ docker pull wongma7/nfs-provisioner:latest
```

## Deploying the provisioner
Now the Docker image is on your machine. Bring up a 1.4+ cluster if you don't have one up already.

```
$ ALLOW_SECURITY_CONTEXT=true API_HOST_IP=0.0.0.0 $GOPATH/src/k8s.io/kubernetes/hack/local-up-cluster.sh
```

Decide on a unique name to give the provisioner that follows the naming scheme `<vendor name>/<provisioner name>`. The provisioner will only provision volumes for claims that request a `StorageClass` with a `provisioner` field set equal to this name. For example, the names of the in-tree GCE and AWS provisioners are `kubernetes.io/gce-pd` and `kubernetes.io/aws-ebs`.

Decide how to run nfs-provisioner and follow one of the below sections. If you want to back your provisioned `PersistentVolumes` with persistent storage (`hostPath` volumes in the provided examples) and want the provisioner pod's NFS server to survive restarts to continue serving them, you should run a deployment, stateful set, or daemon set. Otherwise, you may run a standalone pod. See [here](#a-note-on-deciding-how-to-run) for more help on deciding.

If you are running in OpenShift, see [here](#a-note-on-running-in-openshift) for information on what authorizations the pod needs.

* [In Kubernetes - Pod](#in-kubernetes---pod) 
* [In Kubernetes - StatefulSet of 1 replica](#in-kubernetes---statefulset-of-1-replica)
* [In Kubernetes - Deployment of 1 replica](#in-kubernetes---deployment-of-1-replica)
* [In Kubernetes - DaemonSet](#in-kubernetes---daemonset)
* [Outside of Kubernetes - container](#outside-of-kubernetes---container)
* [Outside of Kubernetes - binary](#outside-of-kubernetes---binary)

Once you finished deploying the provisioner, go to [Usage](usage.md) for info on how to use it.

### In Kubernetes - Pod

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/pod.yaml` to be the provisioner's name you decided on.

Note that you will see provisioning errors with certain Docker storage drivers (`overlay`, `aufs`), because NFS Ganesha requires support for "file handles." To get around this, you may mount an `emptyDir` volume at `/export`, as in `deploy/kube-config/pod_emptydir.yaml`.

Create the pod.

```
$ kubectl create -f deploy/kube-config/pod.yaml
pod "nfs-provisioner" created
```
or
```
$ kubectl create -f deploy/kube-config/pod_emptydir.yaml
pod "nfs-provisioner" created
```
### In Kubernetes - StatefulSet of 1 replica

The procedure for running a stateful set is identical to [that for a deployment, below,](#in-kubernetes---deployment) so wherever you see `deployment` there, replace it with `statefulset`. Note that the service cannot be headless, unlike in most examples of stateful sets.

### In Kubernetes - Deployment of 1 replica

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/deployment.yaml` to be the provisioner's name you decided on. 

`deploy/kube-config/deployment.yaml` specifies a `hostPath` volume `/srv` mounted at `/export`. The `/export` directory is where the provisioner stores its provisioned `PersistentVolumes'` data, so by mounting a volume there, you specify it as the backing storage for provisioned PVs. You may edit the `hostPath` or even mount some other type of volume at `/export`, like a `PersistentVolumeClaim`.

Note that if you continue with the `hostPath` volume, its path must exist on the node the provisioner is scheduled to, so you may want to use a `nodeSelector` to choose a particular node and ensure the directory exists there: `mkdir -p /srv`. If SELinux is enforcing on the node, you may need to make the container [privileged](http://kubernetes.io/docs/user-guide/security-context/) or change the security context of the directory on the node: `sudo chcon -Rt svirt_sandbox_file_t /srv`.

`deploy/kube-config/deployment.yaml` also configures a service. The deployment's pod will use the service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP, because the service's name is passed in via the `SERVICE_NAME` env variable.

Create the deployment and its service.

```
$ kubectl create -f deploy/kube-config/deployment.yaml 
service "nfs-provisioner" created
deployment "nfs-provisioner" created
```

### In Kubernetes - DaemonSet

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/daemonset.yaml` to be the provisioner's name you decided on. 

`deploy/kube-config/daemonset.yaml` specifies a `hostPath` volume `/srv` mounted at `/export`. The `/export` directory is where the provisioner stores its provisioned `PersistentVolumes'` data, so by mounting a volume there, you specify it as the backing storage for provisioned PVs. Each pod in the daemon set does this, effectively creating a "pool" of their nodes' local storage.

`deploy/kube-config/daemonset.yaml` also specifies a `nodeSelector` to target nodes/hosts. Choose nodes to deploy nfs-provisioner on and be sure that the `hostPath` directory exists on each node: `mkdir -p /srv`. If SELinux is enforcing on the nodes, you may need to make the container [privileged](http://kubernetes.io/docs/user-guide/security-context/) or change the security context of the `hostPath` directory on the node: `sudo chcon -Rt svirt_sandbox_file_t /srv`.

`deploy/kube-config/daemonset.yaml` specifies a `hostPort` for NFS, TCP 2049, to expose on the node, so be sure that this port is available on each node. The daemon set's pods will use their node's name as the NFS server IP to put on their `PersistentVolumes`.

Label the chosen nodes to match the `nodeSelector`.

```
$ kubectl label node 127.0.0.1 app=nfs-provisioner
node "127.0.0.1" labeled
```

Create the daemon set.

```
$ kubectl create -f deploy/kube-config/daemonset.yaml 
daemonset "nfs-provisioner" created
```

### Outside of Kubernetes - container

The container is going to need to run with one of `master` or `kubeconfig` set. For the `kubeconfig` argument to work, the config file, and any certificate files it references by path like `certificate-authority: /var/run/kubernetes/apiserver.crt`, need to be inside the container somehow. This can be done by creating Docker volumes, or copying the files into the folder where the Dockerfile is and adding lines like `COPY config /.kube/config` to the Dockerfile before building the image. 

Run nfs-provisioner with `provisioner` equal to the name you decided on, and one of `master` or `kubeconfig` set. It needs to be run with capability `DAC_READ_SEARCH`.

```
$ docker run --cap-add DAC_READ_SEARCH -v $HOME/.kube:/.kube:Z wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -kubeconfig=/.kube/config
```
or
```
$ docker run --cap-add DAC_READ_SEARCH wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -master=http://172.17.0.1:8080
```

You may want to create & mount a Docker volume at `/export` in the container. The `/export` directory is where the provisioner stores its provisioned `PersistentVolumes'` data, so by mounting a volume there, you specify it as the backing storage for provisioned PVs. The volume can then be reused by another container if the original container stops. Without Kubernetes you will have to manage the lifecycle yourself. You should give the container a stable IP somehow so that it can survive a restart to continue serving the shares in the volume.

You may also want to enable per-PV quota enforcement. It is based on xfs project level quotas and so requires that the volume mounted at `/export` be xfs mounted with the prjquota/pquota option. It also requires that it has the privilege to run `xfs_quota`.

With the two above options, the run command will look something like this.

```
$ docker run --privileged -v $HOME/.kube:/.kube:Z -v /xfs:/export:Z wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -kubeconfig=/.kube/config -enable-xfs-quota=true
```

### Outside of Kubernetes - binary

Running nfs-provisioner in this way allows it to manipulate exports directly on the host machine. It will create & store all its data at `/export` so ensure the directory exists and is available for use. It runs assuming the host is already running either NFS Ganesha or a kernel NFS server, depending on how the `use-ganesha` flag is set. Use with caution.

Run nfs-provisioner with `provisioner` equal to the name you decided on, one of `master` or `kubeconfig` set, `run-server` set false, and `use-ganesha` set according to how the NFS server is running on the host. It probably needs to be run as root. 

```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -kubeconfig=$HOME/.kube/config -run-server=false -use-ganesha=false
```
or
```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -master=http://0.0.0.0:8080 -run-server=false -use-ganesha=false
```

You may want to enable per-PV quota enforcement. It is based on xfs project level quotas and so requires that the volume mounted at `/export` be xfs mounted with the prjquota/pquota option. Add the `-enable-xfs-quota=true` argument to enable it.

```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -kubeconfig=$HOME/.kube/config -run-server=false -use-ganesha=false -enable-xfs-quota=true
```

---

#### A note on deciding how to run

* If you want to back your nfs-provisioner's `PersistentVolumes` with persistent storage, you can mount something at the `/export` directory, where the state of the provisioner's NFS server and each PV's data is preserved. In this case you should run a deployment or a stateful set, so that the NFS server can survive restarts and the PVs are more likely to stay usable/mountable for longer than the lifetime of a single nfs-provisioner pod.

    The deployment and stateful set options have the same procedure for running. In both cases the provisioner pod will use a service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP, provided the name of the service is passed in via the `SERVICE_NAME` environment variable. If the pod dies, the deployment or stateful set will start another, which will re-export the folders in `/export` to that same cluster IP.

    Note that stateful sets are in beta and have some limitations, including that their PVs must be provisioned by another PV provisioner. See [the stateful set docs](http://kubernetes.io/docs/user-guide/petset/) for more information.

* Running a daemon set is recommended for a special case of the above. Say you have multiple sources of persistent storage, e.g. the local storage on each node that you can expose to Kubernetes through `hostPath` volumes. Instead of creating multiple deployments or stateful sets for each node, you can simply label each node and run a daemon set. The daemon set's nfs-provisioner pods will use the node's (resolvable) name as the NFS server IP to put on its `PersistentVolumes`, provided the node name is passed in via the `NODE_NAME` environment variable and `hostPort` is specified for the container's NFS port, TCP 2049. Similar to above, if a pod in the set dies, the daemon set will start another, which will re-export the folders in `/export` to the same node name.

* Otherwise, if you don't care to back your nfs-provisioner's `PersistentVolumes` with persistent storage, you can just run a standalone pod. Since in this case the pod is backing PVs with a Docker container layer or `emptyDir` volume, the PVs will only be useful for as long as the pod is running anyway.

#### A note on running in OpenShift

The pod requires authorization to `list` all `StorageClasses`, `PersistentVolumeClaims`, and `PersistentVolumes` in the cluster. 

#### Arguments

* `provisioner` - Name of the provisioner. The provisioner will only provision volumes for claims that request a StorageClass with a provisioner field set equal to this name.
* `master` - Master URL to build a client config from. Either this or kubeconfig needs to be set if the provisioner is being run out of cluster.
* `kubeconfig` - Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.
* `run-server` - If the provisioner is responsible for running the NFS server, i.e. starting and stopping NFS Ganesha. Default true.
* `use-ganesha` - If the provisioner will create volumes using NFS Ganesha (D-Bus method calls) as opposed to using the kernel NFS server ('exportfs'). If run-server is true, this must be true. Default true.
* `grace-period` - NFS Ganesha grace period to use in seconds, from 0-180. If the server is not expected to survive restarts, i.e. it is running as a pod & its export directory is not persisted, this can be set to 0. Can only be set if both run-server and use-ganesha are true. Default 90.
* `enable-xfs-quota` - If the provisioner will set xfs quotas for each volume it provisions. Requires that the directory it creates volumes in ('/export') is xfs mounted with option prjquota/pquota, and that it has the privilege to run xfs_quota. Default false.
