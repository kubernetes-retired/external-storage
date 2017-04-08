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

* Create a claim

```bash
kubectl create -f claim.yaml
```
