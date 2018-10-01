# Examples for the Gluster Subvol provisioner

## Getting Started

Once the `gluster-subvol-provisioner` has been deployed ([see the main
README](../README.md)), a StorageClass can be created. The StorageClass
requires an existing PVC (backed by Gluster) to function. The PVC should be
passed as a parameter to the StorageClass.

The next `.yaml` snippet show how to create a **supervol** PVC from the
`glusterfile` provisioner. It is expected that the provisioner is available.

```yaml
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: supervol
  namespace: storage-gluster
spec:
  storageClassName: glusterfile
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 4Gi
```

Next is defining the StorageClass referencing the Gluster Subvol provisioner
and the newly created **supervol** PVC.

```yaml
---
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: gluster-subvol
provisioner: gluster.org/gluster-subvol
parameters:
  namespace: storage-gluster
  pvc: supervol
```

It is possible to place the **supervol** in a different namespace than the
applications. This can be used to hide it from users views and restrict
permissions.

Once the StorageClass (here called `gluster-subvol`) is created, it is possible
to request PVCs and have them provisioned by the Gluster Subvol provisioner.

```yaml
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: storage-for-my-app
spec:
  accessModes:
    - ReadWriteMany
  resources:
    requests:
      storage: 1Gi
  storageClassName: gluster-subvol
```

This PVC can be used just like any other.


## Cloning

The Gluster Subvol provisioner supports cloning one PVC to a new one. This is
achieved by Annotations in the PersistentVolumeClaim request. The
`k8s.io/CloneRequest` annotation as show below will initiate the duplication of
the contents.

```yaml
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: copy-of-app-data
  annotations:
    k8s.io/CloneRequest: storage-for-my-app
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: gluster-subvol
```

## Practical Example

### Pre-requisites

- a deployed Gluster Subvol provisioner
- a Gluster Subvol StorageClass called 'gluster-subvol'

### Website building and deploying a copy to production

Let's assume there is a website needed that shows a single static page. The
`index.html` will get build on a CentOS system, possibly with `curl`. Normally
this would be done through a fancy application, but that makes automating an
example like this more difficult.

The `webdev-centos.yaml` file contains the creation of a PVC called
`website-dev` and it will be populated by the `centos-webdev` pod (with a
`curl` command).

`webdev-centos.yaml`:
```yaml
---
#
# Minimal PVC where a developer can build a website.
#
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: website-dev
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Mi
  storageClassName: gluster-subvol
---
#
# This pod will just download a fortune phrase and store it (as plain text) in
# index.html on the PVC. This is how we create websites?
#
# The root of the website stored on the above PVC is mounted on /mnt.
#
apiVersion: v1
kind: Pod
metadata:
  name: centos-webdev
spec:
  containers:
  - image: centos:latest
    name: centos
    args:
    - curl
    - -o/mnt/index.html
    - https://api.ef.gy/fortune
    volumeMounts:
    - mountPath: /mnt
      name: website
  # once the website is created, the pod will exit
  restartPolicy: Never
  volumes:
  - name: website
    persistentVolumeClaim:
      claimName: website-dev
```

The building of the website can be done by running:
```shell
$ kubectl apply -f webdev-centos.yaml
```

Once the `centos-webdev` pod has completed, `kubectl` will show an output like:
```shell
$ kubectl get pods
NAME            READY     STATUS        RESTARTS   AGE
centos-webdev   0/1       Completed     0          1m
```

This status signals that the building of the website (well, `index.html`) has
been finished. Now it is possible to clone the PVC for a webservice, without it
being affected by changes to the `website-dev` PVC.

`webdev-nginx.yaml` clones the `website-dev` PVC into a new `website-prod` PVC,
while also starting a NGINX webservice:
```yaml
---
#
# Once the website from website-centos.yaml has been created, the contents can
# be cloned. A new PVC will be provisioned, and the contents from website-dev
# should get cloned.
#
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: website-prod
  annotations:
    k8s.io/CloneRequest: website-dev
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Mi
  storageClassName: gluster-subvol
---
#
# Start a NGINX webserver with the cloned website.
# We'll skip creating a service, to keep things minimal.
#
apiVersion: v1
kind: Pod
metadata:
  name: website-nginx
spec:
  containers:
  - image: gcr.io/google_containers/nginx-slim:0.8
    name: nginx
    ports:
    - containerPort: 80
      name: web
    volumeMounts:
    - mountPath: /usr/share/nginx/html
      name: website
  volumes:
  - name: website
    persistentVolumeClaim:
      claimName: website-prod
```

The `webdev-nginx.yaml` can be applied with `kubectl`:
```shell
$ kubectl apply -f webdev-nginx.yaml
```

After a short period the NGINX pod should get into the Running state:
```shell
$ kubectl get pods -o wide
NAME            READY     STATUS        RESTARTS   AGE       IP           NODE
centos-webdev   0/1       Completed     0          4m        172.17.0.8   minikube
website-nginx   1/1       Running       0          1m        172.17.0.8   minikube
```

As seen in the above output, the NGINX pod listens on the IP-address 172.17.0.8
of node 'minikube' (very useful for testing!). Because no Service has been
defined, the webservice can not be reached from outside the Kubernetes cluster.
In this case the easiest to login on node 'minikube' and check the contents of
the website with `curl`:
```shell
$ minikube ssh curl http://172.17.0.8
Never underestimate the power of somebody with source code, a text editor,
and the willingness to totally hose their system.
		-- Rob Landley <telomerase@yahoo.com>
```
