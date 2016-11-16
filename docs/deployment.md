# Deployment

## Getting the provisioner image
To get the Docker image onto the machine where you want to run nfs-provisioner, you can either build it or pull the latest version from Docker Hub.

### Building
Building the project will only work if the project is in your `GOPATH`. Download the project into your `GOPATH` directory by using `go get` or cloning it manually.

```
$ go get github.com/wongma7/nfs-provisioner
```

Now build the project and the Docker image by running `make container` in the project directory.

```
$ cd $GOPATH/src/github.com/wongma7/nfs-provisioner
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

Decide how to run nfs-provisioner and follow one of the below sections. If you are okay with provisioned `PersistentVolumes` being backed with just a docker container layer, say, for scratch space, you can run a pod. Otherwise, if you want to back provisioned PVs with persistent storage (`hostPath` volumes in the provided examples), so that all PVs' data persists somewhere, you should run a deployment or a daemon set. See [here](#a-note-on-deciding-how-to-run) for more help on deciding.

If you are running in OpenShift, see [here](#a-note-on-running-in-openshift) for information on what authorizations the pod needs.

* [In Kubernetes - Pod](#in-kubernetes---pod) 
* [In Kubernetes - Deployment](#in-kubernetes---deployment)
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

### In Kubernetes - Deployment

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/deployment.yaml` to be the provisioner's name you decided on. 

`deploy/kube-config/deployment.yaml` specifies a `hostPath` volume `/srv` mounted at `/export`. The `/export` directory is where all provisioned `PersistentVolumes'` data is stored, so by mounting a volume there, you specify it as the backing storage for PVs.

`deploy/kube-config/deployment.yaml` also specifies a `nodeSelector` to target a node/host. Choose a node to deploy nfs-provisioner on and be sure that the `hostPath` directory exists on the node: `mkdir -p /srv`. If SELinux is enforcing on the node, you may need to make the container [privileged](http://kubernetes.io/docs/user-guide/security-context/) or change the security context of the `hostPath` directory on the node: `sudo chcon -Rt svirt_sandbox_file_t /srv`.

Label the chosen node to match the `nodeSelector`.

```
$ kubectl label node 127.0.0.1 app=matthew-nfs
node "127.0.0.1" labeled
```

Create the service. The deployment's pod will use the service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP.

```
$ kubectl create -f deploy/kube-config/service.yaml
service "nfs-provisioner" created
```

Create the deployment.

```
$ kubectl create -f deploy/kube-config/deployment.yaml 
deployment "nfs-provisioner" created
```

### In Kubernetes - DaemonSet

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/daemonset.yaml` to be the provisioner's name you decided on. 

`deploy/kube-config/daemonset.yaml` specifies a `hostPath` volume `/srv` mounted at `/export`. The `/export` directory is where all provisioned `PersistentVolumes'` data is stored, so by mounting a volume there, you specify it as the backing storage for PVs. 

`deploy/kube-config/daemonset.yaml` also specifies a `nodeSelector` to target nodes/hosts. Choose nodes to deploy nfs-provisioner on and be sure that the `hostPath` directory exists on each node: `mkdir -p /srv`. If SELinux is enforcing on the nodes, you may need to make the container [privileged](http://kubernetes.io/docs/user-guide/security-context/) or change the security context of the `hostPath` directory on the node: `sudo chcon -Rt svirt_sandbox_file_t /srv`.

`deploy/kube-config/daemonset.yaml` specifies a `hostPort` for NFS, TCP 2049, to expose on the node, so be sure that this port is available on each node. The daemon set's pods will use the node's name as the NFS server IP to put on their `PersistentVolumes`.


Label the chosen nodes to match the `nodeSelector`.

```
$ kubectl label node 127.0.0.1 app=matthew-nfs
node "127.0.0.1" labeled
```

Create the daemon set.

```
$ kubectl create -f deploy/kube-config/daemonset.yaml 
daemonset "nfs-provisioner" created
```

### Outside of Kubernetes - container

The container is going to need to run with one of `master` or `kubeconfig` set. For the `kubeconfig` argument to work, the config file needs to be inside the container somehow. This can be done by creating a Docker volume, or copying the kubeconfig file into the folder where the Dockerfile is and adding a line like `COPY config /.kube/config` to the Dockerfile before building the image.

Run nfs-provisioner with `provisioner` equal to the name you decided on, and one of `master` or `kubeconfig` set. It needs to be run with capability `DAC_READ_SEARCH`.

```
$ docker run --cap-add DAC_READ_SEARCH -v $HOME/.kube:/.kube:Z wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -kubeconfig=/.kube/config
```

or

```
$ docker run --cap-add DAC_READ_SEARCH wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -master=http://172.17.0.1:8080
```

### Outside of Kubernetes - binary

Running nfs-provisioner in this way allows it to manipulate exports directly on the host machine. It runs assuming the host is already running either NFS Ganesha or a kernel NFS server, depending on how the `use-ganesha` flag is set. Use with caution.

Run nfs-provisioner with `provisioner` equal to the name you decided on, one of `master` or `kubeconfig` set, `run-server` set false, and `use-ganesha` set according to how the NFS server is running on the host. It probably needs to be run as root. 

```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -kubeconfig=$HOME/.kube/config -run-server=false -use-ganesha=false
```

or

```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -master=http://0.0.0.0:8080 -run-server=false -use-ganesha=false
```

---

#### A note on deciding how to run

* If you want to back your nfs-provisioner's `PersistentVolumes` with persistent storage, you can mount something at the `/export` directory, where each PV will have its own unique folder containing its data. In this case you should run a deployment targeted by a service, so that the PVs are more likely to stay usable/mountable for longer than the lifetime of a single nfs-provisioner pod. The deployment's nfs-provisioner pod will use the service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP, provided the name of the service is passed in via the `SERVICE_NAME` environment variable. And if the pod dies, the deployment will start another, which will re-export the folders in `/export` to that same cluster IP.

* Running a daemon set is recommended for a special case of the above. Say you have multiple sources of persistent storage, e.g. the local storage on each node that you can expose to Kubernetes through `hostPath` volumes. Instead of creating multiple pairs of deployments and services on each node, you can simply label each node and run a daemon set. The daemon set's nfs-provisioner pods will use the node's (resolvable) name as the NFS server IP to put on its `PersistentVolumes`, provided the node name is passed in via the `NODE_NAME` environment variable and `hostPort` is specified for the container's NFS port, TCP 2049. Similar to above, if a pod in the set dies, the daemon set will start another, which will re-export the folders in `/export` to the same node name.

* Otherwise, if you don't care to back your nfs-provisioner's `PersistentVolumes` with persistent storage, there is no reason to use a service and you can just run a pod. Since in this case the pod is backing PVs with a Docker container layer, the PVs will only be useful for as long as the pod is running anyway.

#### A note on running in OpenShift

The pod requires authorization to `list` all `StorageClasses`, `PersistentVolumeClaims`, and `PersistentVolumes` in the cluster. 

#### Arguments

* `provisioner` - Name of the provisioner. The provisioner will only provision volumes for claims that request a StorageClass with a provisioner field set equal to this name.
* `master` - Master URL to build a client config from. Either this or kubeconfig needs to be set if the provisioner is being run out of cluster.
* `kubeconfig` - Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.
* `run-server` - If the provisioner is responsible for running the NFS server, i.e. starting and stopping NFS Ganesha. Default true.
* `use-ganesha` - If the provisioner will create volumes using NFS Ganesha (D-Bus method calls) as opposed to using the kernel NFS server ('exportfs'). If run-server is true, this must be true. Default true.
