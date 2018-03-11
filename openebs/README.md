# OpenEBS Kubernetes PV provisioner

## About OpenEBS

OpenEBS is containerized storage for containers. More details on OpenEBS can be found on [OpenEBS project page.](https://github.com/openebs/openebs)
OpenEBS Kubernetes PV provisioner is using API's exposed by [maya-apiserver](https://github.com/openebs/mayaserver) to perform provision and delete operation.

## Building OpenEBS provisioner from source

### Generate openebs-provisioner binary

Following command will generate `openebs-provisioner` binary in external-storage/openebs.

```
$ make openebs
```

### Create a docker image on local

```
$ make push-openebs-provisioner
```

### Push OpenEBS provisioner image to docker hub

To push docker image to docker hub you need to have docker hub login credentials. You can pass docker credentials and image name as a environment variable.

```
$ export DIMAGE="docker-username/imagename"
$ export DNAME="docker-username"
$ export DPASS="docker-hub-password"
$ make deploy-openebs-provisioner
```

## Running OpenEBS provisioner in kubernetes cluster

OpenEBS provisioner is one of the component of [OpenEBS Operator](https://github.com/openebs/openebs/blob/master/k8s/openebs-operator.yaml). You can run OpenEBS provisioner by starting the OpenEBS operator.

```
$ kubectl apply -f https://raw.githubusercontent.com/openebs/openebs/master/k8s/openebs-operator.yaml
```

If you want to run specific version of OpenEBS provisioner then you need to follow steps given below:

- Create OpenEBS Provisioner as kubernetes deployment object in OpenEBS Operator.


```yaml
---

apiVersion: apps/v1beta1
kind: Deployment
metadata:
  name: openebs-provisioner
  namespace: openebs
spec:
  replicas: 1
  template:
    metadata:
      labels:
        name: openebs-provisioner
    spec:
      serviceAccountName: openebs-maya-operator
      containers:
      - name: openebs-provisioner
        imagePullPolicy: Always
        image: openebs/openebs-k8s-provisioner:0.3-RC2
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: OPENEBS_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace

```

- Set the OpenEBS Provisioner as the provisioner in kubernetes storageclass specs. Please refer [openebs-storageclasses.yaml](https://github.com/openebs/openebs/blob/master/k8s/openebs-storageclasses.yaml) .


```yaml
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
   name: openebs-jupyter
provisioner: openebs.io/provisioner-iscsi
parameters:
  pool: hostdir-var
  replica: "2"
  size: 5G
```

- You can claim this volume by setting above storageclass in PersistentVolumeClaim Specs. Please refer [demo-jupyter-openebs.yaml](https://github.com/openebs/openebs/blob/master/k8s/demo/jupyter/demo-jupyter-openebs.yaml)


```yaml
---
kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: jupyter-data-vol-claim
spec:
  storageClassName: openebs-jupyter
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5G
---
apiVersion: v1
kind: Service
metadata:
  name: jupyter-service
spec:
  ports:
  - name: ui
    port: 8888
    nodePort: 32424
    protocol: TCP
  selector:
    name: jupyter-server
  sessionAffinity: None
  type: NodePort
```

- Finally, Run OpenEBS inside kubernetes cluster on [local](https://github.com/openebs/openebs/blob/master/k8s/hyperconverged/tutorial-configure-openebs-local.md) and on [Google cloud engine](https://github.com/openebs/openebs/blob/master/k8s/hyperconverged/tutorial-configure-openebs-gke.md) with above changes.
