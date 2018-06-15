# Kubernetes external FLEX provisioner

This is an example external provisioner for kubernetes meant for use with FLEX based volume plugins.  The provisioner runs in a pod, so the shell script which provisions/deletes the volumes must also be included in the POD's container (and not on the host).

**First Steps**

Before building and packaging this, you need to include the shell script which flex will use for provisioning.  The shell script path must match what's in the provisioning container.

The current example is in flex/deploy/docker and is specified in examples/pod-provisioner.yaml here:
*- "-execCommand=/opt/storage/flex-provision.sh"*
If you copy in a new file or change the path, update the flag in the pod yaml.

**To Build**

```bash
make
```

**To Deploy**

You can use the example provisioner pod to deploy:

```
mkdir -p /usr/libexec/kubernetes/kubelet-plugins/volume/exec/flex
cp deploy/flex-provision.sh /usr/libexec/kubernetes/kubelet-plugins/volume/exec/flex/flex
chmod ugo+x /usr/libexec/kubernetes/kubelet-plugins/volume/exec/flex/flex

kubectl create -f deploy/manifests/pod-provisioner.yaml \
               -f deploy/manifests/rbac.yaml \
               -f deploy/manifests/sc.yaml
```

You can test it with:
```
kubectl create -f examples/pvc.yaml -f examples/pod-application.yaml
```
