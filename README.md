# nfs-provisioner
nfs-provisioner is an out-of-tree dynamic provisioner for Kubernetes. It automatically creates NFS `PersistentVolumes` for `PersistentVolumeClaims` that request a `StorageClass` configured to use some instance of nfs-provisioner as their provisioner. For more information see http://kubernetes.io/docs/user-guide/persistent-volumes/ and https://github.com/kubernetes/kubernetes/pull/30285.

>Currently, the provisioner creates the NFS shares that back provisioned `PersistentVolumes` by making unique, deterministically named directories in `/export` for each volume. No quotaing or security/permissions yet.

## Deployment

Build nfs-provisioner and a Docker image for it.

```
$ make container
```

Decide on a unique name to give the provisioner that follows the naming scheme `<vendor name>/<provisioner name>`. The provisioner will only provision volumes for claims that request a `StorageClass` with a provisioner field set equal to this name.

Decide how to run nfs-provisioner. It can be run in Kubernetes as a pod or outside of Kubernetes as a standalone container.

* If you want to back your nfs-provisioner's exports with persistent storage, by mounting something at the `/export` directory it provisions out of, you should run it as a deployment with a service so that the provisioned `PersistentVolumes` are more likely to stay usable/mountable for longer than the lifetime of a single nfs-provisioner pod. A nfs-provisioner pod can use a service's cluster IP as the NFS server IP to put on its `PersistentVolumes`, instead of its own unstable pod IP, if the name of a service targeting it is passed in via the `MY_SERVICE_NAME` environment variable. Additionally, because nfs-provisioner uses an NFS Ganesha configuration file at `/export/_vfs.conf`, if one pod dies and the deployment starts another, the new pod will reuse the config file and directories will be re-exported.
> Note that this setup isn't foolproof & can't overcome the "limitations" of NFS, e.g. if the server dies while a client had something open, expect `stale file handle` errors.

* Otherwise, if you don't care to back your nfs-provisioner's exports with persistent storage, there is no reason to use a service and you can just run it as a pod. Since in this case the pod is provisioning out of ephemeral storage inside the container, the `PersistentVolumes` it provisions will only be useful for as long as the pod is running anyway.

### In Kubernetes - Pod

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/pod.yaml` to be the provisioner's name you decided on. Create the pod.

```
$ kubectl create -f deploy/kube-config/pod.yaml
pod "nfs-provisioner" created
```

### In Kubernetes - Deployment

Edit the `provisioner` argument in the `args` field in `deploy/kube-config/deployment.yaml` to be the provisioner's name you decided on. 

`deploy/kube-config/deployment.yaml` specifies a `hostPath` volume and a `nodeSelector`. Pick a node to deploy nfs-provisioner on and label it to match the `nodeSelector`.

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

### Arguments 
* `provisioner` - Name of the provisioner. The provisioner will only provision volumes for claims that request a StorageClass with a provisioner field set equal to this name.
* `out-of-cluster` - If the provisioner is being run out of cluster. Set the master or kubeconfig flag accordingly if true. Default false.
* `master` - Master URL to build a client config from. Either this or kubeconfig needs to be set if the provisioner is being run out of cluster.
* `kubeconfig` - Absolute path to the kubeconfig file. Either this or master needs to be set if the provisioner is being run out of cluster.
* `run-server` - If the provisioner is responsible for running the NFS server, i.e. starting and stopping NFS Ganesha. Default true.

## Usage

The nfs-provisioner has been deployed and is now watching for claims it should provision volumes for. No such claims can exist until a properly configured `StorageClass` for claims to request is created.

Edit the `provisioner` field in `deploy/kube-config/class.yaml` to be the provisioner's name. The nfs-provisioner as written doesn't take any `parameters` and will be unable to provision if any are specified, so don't specify any. Name the `StorageClass` however you like; the name is how claims will request this class. Create the class.
 
```
$ kubectl create -f deploy/kube-config/class.yaml
storageclass "matthew" created
```

Now if everything is working correctly, when you create a claim requesting the class you just created, the provisioner will automatically create a volume.

Edit the `volume.beta.kubernetes.io/storage-class` annotation in `deploy/kube-config/claim.yaml` to be the name of the class. Create the claim.

```
$ kubectl create -f deploy/kube-config/claim.yaml
persistentvolumeclaim "nfs" created
```

The nfs-provisioner provisions a PV for the PVC you just created.

```
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM         REASON    AGE
pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           Delete          Bound       default/nfs             23s
```

### Using as default

The provisioner can be used as the default storage provider, meaning claims that don't request a `StorageClass` get volumes provisioned for them by the provisioner by default. To set as the default a `StorageClass` that specifies the provisioner, turn on the `DefaultStorageClass` admission-plugin and add the `storageclass.beta.kubernetes.io/is-default-class` annotation to the class. See http://kubernetes.io/docs/user-guide/persistent-volumes/#class-1 for more information.
