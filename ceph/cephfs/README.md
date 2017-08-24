# CephFS Volume Provisioner for Kubernetes 1.5+

[![Docker Repository on Quay](https://quay.io/repository/external_storage/cephfs-provisioner/status "Docker Repository on Quay")](https://quay.io/repository/external_storage/cephfs-provisioner)

Using Ceph volume client

# Test instruction

Compile the provisioner
``` console
make
```

Make the container image and push to the registry
``` console
make push
```

* Start Kubernetes local cluster

* Create a Ceph admin secret

```bash
ceph auth get client.admin 2>&1 |grep "key = " |awk '{print  $3'} |xargs echo -n > /tmp/secret
kubectl create secret generic ceph-secret-admin --from-file=/tmp/secret --namespace=kube-system
```

* Start CephFS provisioner

The following example uses `cephfs-provisioner-1` as the identity for the instance and assumes kubeconfig is at `/root/.kube`. The identity should remain the same if the provisioner restarts. If there are multiple provisioners, each should have a different identity.

```bash
docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host  cephfs-provisioner /usr/local/bin/cephfs-provisioner -master=http://127.0.0.1:8080 -kubeconfig=/kube/config -id=cephfs-provisioner-1
```
Alternatively, start a deployment:

```bash
kubectl create -f deployment.yaml
```

* Create a CephFS Storage Class

Replace Ceph monitor's IP in [class.yaml](class.yaml) with your own and create storage class:

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

# Acknowledgement
Inspired by CephFS Manila provisioner and conversation with John Spray
