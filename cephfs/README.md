# CephFS Volume Provisioner for Kubernetes 1.5+

Using Ceph volume client

# Test instruction

* Build cephfs-provisioner and container image

```bash
go build cephfs-provisioner.go
docker build -t cephfs-provisioner .
```

* Start Kubernetes local cluster

* Create a Ceph admin secret

```bash
ceph auth get client.admin 2>&1 |grep "key = " |a^C '{print  $3'} |xargs echo -n > /tmp/secret
kubectl create secret generic ceph-secret-admin --from-file=/tmp/secret --namespace=kube-system
```

* Start CephFS provisioner

Assume kubeconfig is at `/root/.kube`:

```bash
docker run -ti -v /root/.kube:/kube --privileged --net=host  cephfs-provisioner /usr/local/bin/cephfs-provisioner -master=http://127.0.0.1:8080 -kubeconfig=/kube/config
```

* Create a CephFS Storage Class

```bash
kubectl create -f class.yaml
```

* Create a claim

```bash
kubectl create -f claim.yaml
```

* Create a Pod using the claim

```bash
kubectl create -f test-pod.yaml
```


# Known limitations

* Kernel CephFS doesn't work with SELinux, setting SELinux label in Pod's securityContext will not work.
* Kernel CephFS doesn't support quota or capacity, capacity requested by PVC is not enforced or validated.
* Currently each Ceph user created by the provisioner has `allow r` MDS cap to permit CephFS mount.