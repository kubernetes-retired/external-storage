# Deployment

## Getting the provisioner image
To get the Docker image onto your machine you can either build it or pull the latest version from Docker Hub.

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
$ ALLOW_PRIVILEGED=true ALLOW_SECURITY_CONTEXT=true API_HOST=0.0.0.0 KUBE_ENABLE_CLUSTER_DNS=true hack/local-up-cluster.sh
```

Decide on a unique name to give the provisioner that follows the naming scheme `<vendor name>/<provisioner name>`. The provisioner will only provision volumes for claims that request a `StorageClass` with a `provisioner` field set equal to this name. For example, the names of the in-tree GCE and AWS provisioners are `kubernetes.io/gce-pd` and `kubernetes.io/aws-ebs`.

Decide how to run nfs-provisioner and follow one of the below sections. See [here](#a-note-on-deciding-how-to-run) for help on deciding between a pod and deployment; in short, if you want to back your shares with persistent storage, running a deployment & service has some benefits. If you are running in OpenShift, see [here](#a-note-on-running-in-openshift) for information on what authorizations the pod needs.

* [In Kubernetes - Pod](#in-kubernetes---pod)
* [In Kubernetes - Deployment](#in-kubernetes---deployment)
* [Outside of Kubernetes - container](#outside-of-kubernetes---container)
* [Outside of Kubernetes - binary](#outside-of-kubernetes---binary)

Once you finished deploying the provisioner, go to [Usage](usage.md) for info on how to use it.

### In Kubernetes - Pod

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/pod.yaml` to be the provisioner's name you decided on. Create the pod.

```
$ kubectl create -f deploy/kube-config/pod.yaml
pod "nfs-provisioner" created
```

### In Kubernetes - Deployment

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/deployment.yaml` to be the provisioner's name you decided on. 

`deploy/kube-config/deployment.yaml` specifies a `hostPath` volume and a `nodeSelector`. You can substitute the `hostPath` volume with your own persistent storage if you like, just mount it at `/export`. Pick a node to deploy nfs-provisioner on and label it to match the `nodeSelector`.  Be sure that the hostPath directory exists on each target host (/exports by default): `mkdir -p /exports`

```
$ kubectl label node 127.0.0.1 app=matthew-nfs
node "127.0.0.1" labeled
```

Create the service.

```
$ kubectl create -f deploy/kube-config/service.yaml
service "nfs-provisioner" created
```

Create the deployment.

```
$ kubectl create -f deploy/kube-config/deployment.yaml 
deployment "nfs-provisioner" created
```

### Outside of Kubernetes - container

The container is going to need to run with `out-of-cluster` set true and one of `master` or `kubeconfig` set. For the `kubeconfig` argument to work, the config file needs to be inside the container somehow. This can be done by copying the kubeconfig file into the folder where the Dockerfile is and adding a line like `COPY config /config` to the Dockerfile before building the image.  

Run nfs-provisioner with `provisioner` equal to the name you decided on, `out-of-cluster` set true and one of `master` or `kubeconfig` set. It needs to be run with Docker's `privileged` flag.

```
$ docker run --privileged wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -out-of-cluster=true -kubeconfig=/config
```

or

```
$ docker run --privileged wongma7/nfs-provisioner:latest -provisioner=matthew/nfs -out-of-cluster=true -master=http://172.17.0.1:8080
```

### Outside of Kubernetes - binary

Running nfs-provisioner in this way allows it to manipulate exports directly on the host machine. It runs assuming the host is already running either NFS Ganesha or a kernel NFS server, depending on how the `use-ganesha` flag is set. Use with caution.

Run nfs-provisioner with `provisioner` equal to the name you decided on, `out-of-cluster` set true, one of `master` or `kubeconfig` set, `run-server` set false, and `use-ganesha` set according to how the NFS server is running on the host. It probably needs to be run as root. 

```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -out-of-cluster=true -kubeconfig=/config -run-server=false -use-ganesha=false
```

or

```
$ sudo ./nfs-provisioner -provisioner=matthew/nfs -out-of-cluster=true -master=http://172.17.0.1:8080 -run-server=false -use-ganesha=false
```

---

#### A note on deciding how to run

* If you want to back your nfs-provisioner's exports with persistent storage, you can mount something at the `/export` directory, where the provisioner creates unique directories for each provisioned volume. In this case you should run it as a deployment with a service so that the provisioned `PersistentVolumes` are more likely to stay usable/mountable for longer than the lifetime of a single nfs-provisioner pod. A nfs-provisioner pod can use a service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP, if the name of a service targeting it is passed in via the `MY_SERVICE_NAME` environment variable. Because nfs-provisioner uses an NFS Ganesha configuration file at `/export/vfs.conf`, if one pod dies and the deployment starts another, the new pod will reuse the config file and directories will be re-exported to the same cluster IP.

* Otherwise, if you don't care to back your nfs-provisioner's exports with persistent storage, there is no reason to use a service and you can just run it as a pod. Since in this case the pod is provisioning out of ephemeral storage inside the container, the `PersistentVolumes` it provisions will only be useful for as long as the pod is running anyway.

#### A note on running in OpenShift

The pod requires authorization to `list` all `StorageClasses`, `PersistentVolumeClaims`, and `PersistentVolumes` in the cluster. 

#### Arguments

* `provisioner` - Name of the provisioner. The provisioner will only provision volumes for claims that request a StorageClass with a provisioner field set equal to this name.
* `out-of-cluster` - If the provisioner is being run out of cluster. Set the master or kubeconfig flag accordingly if true. Default false.
* `master` - Master URL to build a client config from. Either this or kubeconfig needs to be set if the provisioner is being run out of cluster.
* `kubeconfig` - Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.
* `run-server` - If the provisioner is responsible for running the NFS server, i.e. starting and stopping NFS Ganesha. Default true.
* `use-ganesha` - If the provisioner will create volumes using NFS Ganesha (D-Bus method calls) as opposed to using the kernel NFS server ('exportfs'). If run-server is true, this must be true. Default true.
