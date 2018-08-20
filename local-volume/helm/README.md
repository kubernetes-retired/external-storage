# Helm installation procedure 

## Overview

In order to be able to use **helm** to render templates, it has to be installed on a host where a user plans
to generate templates.

## Helm Installation
On Linux or Mac OS, run these two commands to download and copy helm binary into /usr/local/bin directory.

``` console
export HELM_URL=http://storage.googleapis.com/kubernetes-helm/helm-v2.7.2-$(uname | tr A-Z a-z)-amd64.tar.gz
curl "$HELM_URL" | sudo tar --strip-components 1 -C /usr/local/bin -zxf - $(uname | tr A-Z a-z)-amd64/helm
```
Provisioner's spec generation process has been tested with helm version 2.7.2.

## Helm verification

Run the following command:
``` console
helm version
```

This output should be generated:
``` console
Client: &version.Version{SemVer:"v2.7.2", GitCommit:"8478fb4fc723885b155c924d1c8c410b7a9444e6", GitTreeState:"clean"}
Error: cannot connect to Tiller
``` 

The error on the second line of the output can be ignored, as Tiller, helm's deployment engine has not been installed on the 
kubernetes cluster as it is not required for template generation.

## Provisioner's helm chart

Helm templating is used to generate the provisioner's DaemonSet and ConfigMap specs.
The generated specs can be further customized as needed (usually not necessary), and then deployed using kubectl.

**helm template** uses 3 sources of information:
1. Provisioner's chart template located at helm/provisioner/templates/provisioner.yaml
2. Provisioner's default values.yaml which contains variables used for rendering a template.
3. (Optional) User's customized values.yaml as a part of helm template command. User's provided
   values will override default values of Provisioner's values.yaml.

Default values.yaml is located in local-volume/helm/provisioner folder, user should not remove variables from this file but can
change any values of these variables.

## Configurations

The following table lists the configurable parameters of the local volume
provisioner chart and their default values.

| Parameter                              | Description                                                                                           | Type     | Default                                                    |
| ---                                    | ---                                                                                                   | ---      | ---                                                        |
| common.rbac                            | Generating RBAC (Role Based Access Control) objects.                                                  | bool     | `true`                                                     |
| common.namespace                       | Namespace where provisioner runs.                                                                     | str      | `default`                                                  |
| common.useAlphaAPI                     | If running against pre-1.10 k8s version, the `useAlphaAPI` flag must be enabled.                      | bool     | `false`                                                    |
| common.useJobForCleaning               | If set to true, provisioner will use jobs-based block cleaning.                                       | bool     | `false`                                                    |
| common.useNodeNameOnly                 | If set to true, provisioner name will only use Node.Name and not Node.UID.                            | bool     | `false`                                                    |
| common.minResyncPeriod                 | Resync period in reflectors will be random between `minResyncPeriod` and `2*minResyncPeriod`.         | str      | `5m0s`
| common.configMapName                   | Provisioner ConfigMap name.                                                                           | str      | `local-provisioner-config`                                 |
| classes.[n].name                       | StorageClass name.                                                                                    | str      | `-`                                                        |
| classes.[n].hostDir                    | Path on the host where local volumes of this storage class are mounted under.                         | str      | `-`                                                        |
| classes.[n].mountDir                   | Optionally specify mount path of local volumes. By default, we use same path as hostDir in container. | str      | `-`                                                        |
| classes.[n].blockCleanerCommand        | List of command and arguments of block cleaner command.                                               | list     | `-`                                                        |
| classes.[n].storageClass               | Create storage class for this class and configure it optionally.                                      | bool/map | `false`                                                    |
| classes.[n].storageClass.reclaimPolicy | Specify reclaimPolicy of storage class, available: Delete/Retain.                                     | str      | `Delete`                                                   |
| daemonset.name                         | Provisioner DaemonSet name.                                                                           | str      | `local-volume-provisioner`                                 |
| daemonset.image                        | Provisioner image.                                                                                    | str      | `quay.io/external_storage/local-volume-provisioner:v2.1.0` |
| daemonset.imagePullPolicy              | Provisioner DaemonSet image pull policy.                                                              | str      | `-`                                                        |
| daemonset.serviceAccount               | Provisioner DaemonSet service account.                                                                | str      | `local-storage-admin`                                      |
| daemonset.kubeConfigEnv                | Specify the location of kubernetes config file.                                                       | str      | `-`                                                        |
| daemonset.nodeLabels                   | List of node labels to be copied to the PVs created by the provisioner.                               | list     | `-`                                                        |
| daemonset.tolerations                  | List of tolerations to be applied to the Provisioner DaemonSet.                                       | list     | `-`                                                        |
| daemonset.resources                    | Map of resource request and limits to be applied to the Provisioner Daemonset.                        | map     | `-`                                                        |
Note: `classes` is a list of objects, you can specify one or more classes.

## Examples

To try out one of the [examples](examples/). you can run, e.g. for gce:

```console
$ helm template ./helm/provisioner -f helm/examples/gce.yaml > ./provisioner/deployment/kubernetes/provisioner_generated.yaml
```

Currently you can try the following examples:

* [examples/baremetal-cleanbyjobs.yaml](examples/baremetal-cleanbyjobs.yaml)
* [examples/baremetal-resyncperiod.yaml](examples/baremetal-resyncperiod.yaml)
* [examples/baremetal-tolerations.yaml](examples/baremetal-tolerations.yaml)
* [examples/baremetal-with-resource-limits.yaml](examples/baremetal-with-resource-limits.yaml)
* [examples/baremetal-without-rbac.yaml](examples/baremetal-without-rbac.yaml)
* [examples/baremetal.yaml](examples/baremetal.yaml)
* [examples/gce-pre1.9.yaml](examples/gce-pre1.9.yaml)
* [examples/gce-retain.yaml](examples/gce-retain.yaml)
* [examples/gce.yaml](examples/gce.yaml)
* [examples/gke.yaml](examples/gke.yaml)
