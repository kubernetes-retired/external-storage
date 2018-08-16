# GlusterFS Simple Provisioner for Kubernetes 1.5+

GlusterFS simple provisioner is an external provisioner which
dynamically provision glusterfs volumes on demand.

Unlike [Heketi][1], this provisioner will not manage GlusterFS cluster.
So that you must manage GlusterFS cluster by yourself. This will simply
create Gluster volume in the specified GlusterFS cluster like nfs-client
provisioner.

It means, for example, if you want to add brick to your Gluster volume,
you can use familiar `gluster vol add-brick` command.

[1]: https://github.com/heketi/heketi

## Build GlusterFS Simple Provisioner and container image

```bash
[root@localhost]# make container
```

## Start Kubernetes local cluster

## Start GlusterFS cluster on Kubernetes

GlusterFS Simple Provisioner requires Gluster cluster which is running on the top of Kubernetes cluster.

```bash
[root@localhost]# kubectl create -f deploy/glusterfs-daemonset.yaml
[root@localhost]# kubectl label node <...node...> storagenode=glusterfs
```
### Configure the GlusterFS trusted pool

GlusterFS Simple Provisioner will not manage GlusterFS cluster at all, so it is needed to manage GlusterFS cluster by yourself.

Check GlusterFS node's `podIP`s.

```bash
[root@localhost]# kubectl get pods -o wide --selector=glusterfs-node=pod
NAME              READY     STATUS    RESTARTS   AGE       IP             NODE
glusterfs-grck0   1/1       Running   0          11m       172.16.2.132   worker02
glusterfs-mgmnd   1/1       Running   0          11m       172.16.2.131   worker01
```

And add nodes to trusted pool

```bash
[root@localhost]# kubectl exec -ti glusterfs-grck0 gluster peer probe 172.16.2.131
[root@localhost]# kubectl exec -ti glusterfs-mgmnd gluster peer probe 172.16.2.132
```

## Configure RBAC (Kubernetes 1.8+)

If you are running Kubernetes 1.8+, you will need to bind a set of permissions to a new ServiceAccount for the provisioner to access the Kubernetes API.

Run the following to configure RBAC for a new `glfs-provisioner` ServiceAccount:
```bash
kubectl create -f deploy/rbac.yaml
```

NOTE: Make sure that your deployment contains a reference to `serviceAccount: glfs-provisioner`.

## Start glusterfs simple provisioner

The following example assumes kubeconfig is at `/root/.kube`.

```bash
docker run -ti \
           -v /root/.kube:/kube \
           -v /var/run/kubernetes:/var/run/kubernetes \
           external_storage/glusterfs-simple-provisioner \
              -kubeconfig=/kube/config
```

## Create a glusterfs-simple Storage Class

```bash
echo 'kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: glusterfs-simple
provisioner: gluster.org/glusterfs-simple
parameters:
  forceCreate: "true"
  brickrootPaths: "172.16.2.131:/tmp/,172.16.2.132:/tmp"' | kubectl create -f -
```

The available storage class parameter are listed below:

```yaml

parameters:
    brickRootPaths: "172.16.2.131:/tmp/,172.16.2.132:/tmp"
    volumeType: "replica 2"
    namespace: "default"
    selector: "glusterfs-node==pod"
    forceCreate: "true"
```

* `brickRootPaths`: Bricks will be created under this directories.
* `volumeType`: Storage class will create this type of volume.
* `namespace`: Namespace which GlusterFS pods are consisted.
* `selector`: Label selector which will specify GlusterFS pods.
* `forceCreate`: If true, glusterd create volume forcefully.

