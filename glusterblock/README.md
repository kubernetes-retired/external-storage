# glusterblock Volume Provisioner for Kubernetes 1.5+


# Test instruction

* Build glusterblock-provisioner and container image

```bash
go build glusterblock-provisioner.go
docker build -t glusterblock-provisioner .
```

* Start Kubernetes local cluster

* Start glusterblock provisioner

Assume kubeconfig is at `/root/.kube`:

```bash
docker run -ti -v /root/.kube:/kube --privileged --net=host  glusterblock-provisioner /usr/local/bin/glusterblock-provisioner -master=http://127.0.0.1:8080 -kubeconfig=/kube/config
```

* Create a glusterblock Storage Class

```bash
kubectl create -f class.yaml
```

* Create a claim

```bash
kubectl create -f claim.yaml
```
