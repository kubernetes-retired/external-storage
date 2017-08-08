# OpenEBS Kubernetes PV provisioner

## About OpenEBS 

OpenEBS is containerized storage for containers. More details on OpenEBS can be found on [OpenEBS project page](https://github.com/openebs/openebs)


## Building OpenEBS provisioner from source

### Generate openebs-provisioner binary

Following command will generate `openebs-provisioner` binary in external-storage/openebs.

```
$ make openebs
```

### Create a docker image on local

```
$ make push-openebs-provisoner
```

### Push OpenEBS provisioner image to docker hub

To push docker image to docker hub you need to have docker hub login credentials. You can pass docker credentials and image name as a environment variable.

```
$ export DIMAGE="docker-username/imagename"
$ export DNAME="docker-username"
$ export DPASS="docker-hub-password"
$ make deploy-openebs-provisioner
```

## OpenEBS provisioner in kubernetes cluster

You can include your docker image in your `.yaml` file. Please refer [openebs-operator.yaml](https://github.com/openebs/openebs/blob/master/k8s/openebs-operator.yaml#L86) .