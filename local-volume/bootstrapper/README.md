# local-volume-provisioner-bootstrap

local-volume-provisioner-bootstrap is used to bootstrap provisioner. The main use
case of bootstrapper is to make provisioner configurable. It configures provisioner
based on user config, creates appropriate service account, role bindings, then starts
provisioner. It will exit as soon as provisioner is successfully created.

## Development

Compile the bootstrapper:

```console
make
```

Deploy to existing cluster:

```console
kubectl create -f deployment/kubernetes/example-config.yaml
kubectl create -f deployment/kubernetes/admin-account.yaml
kubectl create -f deployment/kubernetes/bootstrapper.yaml
```

## TODO

- Clean up resources upon error
- Volume config parameter `mountDir` can be auto-generated
- Update local volume docs (document service account and rolebindings)
