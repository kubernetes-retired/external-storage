# Helm installation procedure 

## Overview

In order to be able to use **helm** to render templates, it has to be installed on a host where a user plans
to generate templates.

## Helm Installation
On Linux, run these two commands to download and copy helm binary into /usr/bin directory.

``` console
export HELM_URL=http://storage.googleapis.com/kubernetes-helm/helm-v2.7.2-linux-amd64.tar.gz
curl "$HELM_URL" | sudo tar --strip-components 1 -C /usr/bin linux-amd64/helm -zxf -
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
