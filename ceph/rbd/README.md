# RBD Volume Provisioner for Kubernetes 1.5+

`rbd-provisioner` is an out-of-tree dynamic provisioner for Kubernetes 1.5+.
You can use it quickly & easily deploy ceph RBD storage that works almost
anywhere. 

It works just like in-tree dynamic provisioner. For more information on how
dynamic provisioning works, see [the docs](http://kubernetes.io/docs/user-guide/persistent-volumes/)
or [this blog post](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html).

## Development

Compile the provisioner

```console
make
```

Make the container image and push to the registry

```console
make push
```

## Test instruction

* Start Kubernetes local cluster

See https://kubernetes.io/.

* Install the rbd package on the worker nodes (fixes #1256)

for example, on debian the package is ceph-common

* Create a Ceph admin secret

```bash
ceph auth get client.admin 2>&1 |grep "key = " |awk '{print  $3'} |xargs echo -n > /tmp/key
kubectl create secret generic ceph-secret-admin --from-file=/tmp/key --namespace=kube-system --type=kubernetes.io/rbd
```

* Create a Ceph pool and a user secret

```bash
ceph osd pool create kube 8 8
ceph auth add client.kube mon 'allow r' osd 'allow rwx pool=kube'
ceph auth get-key client.kube > /tmp/key
kubectl create secret generic ceph-secret --from-file=/tmp/key --namespace=kube-system --type=kubernetes.io/rbd
```

* Start RBD provisioner

The following example uses `rbd-provisioner-1` as the identity for the instance and assumes kubeconfig is at `/root/.kube`. The identity should remain the same if the provisioner restarts. If there are multiple provisioners, each should have a different identity.

```bash
docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host quay.io/external_storage/rbd-provisioner /usr/local/bin/rbd-provisioner -master=http://127.0.0.1:8080 -kubeconfig=/kube/config -id=rbd-provisioner-1
```

Alternatively, deploy it in kubernetes, see [deployment](deploy/README.md).

* Create a RBD Storage Class

Replace Ceph monitor's IP in [examples/class.yaml](examples/class.yaml) with your own and create storage class:

```bash
kubectl create -f examples/class.yaml
```

* Create a claim

```bash
kubectl create -f examples/claim.yaml
```

* Create a Pod using the claim

```bash
kubectl create -f examples/test-pod.yaml
```

## Acknowledgements

- This provisioner is extracted from [Kubernetes core](https://github.com/kubernetes/kubernetes) with some modifications for this project.
