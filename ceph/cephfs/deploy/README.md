# Deployment

## Table of contents

* [Deployment Environment variables](#deployment-environment-variables)
* [Install without RBAC roles](#install-without-rbac-roles)
* [Install with RBAC roles](#install-with-rbac-roles)

## Deployment environment variables
|Parameter|Description|Default|
|---|---|---|
|PROVISIONER_NAME | Name of the provisioner. If you change this, you also have to change it in the StorageClass `provisioner` field. | ceph.com/cephfs|
|PROVISIONER_SECRET_NAMESPACE | The namespace to which the secrets will be deployed. If this differs from the namespace where the rolebinding is deployed you have to adjust the role and rolebinding or use a clusterrole. | PVC namespace|

## Install without RBAC roles

```
cd $GOPATH/src/github.com/kubernetes-incubator/external-storage/ceph/cephfs/deploy
kubectl apply -f ./non-rbac
```

## Install with RBAC roles

```
cd $GOPATH/src/github.com/kubernetes-incubator/external-storage/ceph/cephfs/deploy
NAMESPACE=cephfs # change this if you want to deploy it in another namespace
sed -r -i "s/namespace: [^ ]+/namespace: $NAMESPACE/g" ./rbac/*.yaml
sed -r -i "N;s/(name: PROVISIONER_SECRET_NAMESPACE.*\n[[:space:]]*)value:.*/\1value: $NAMESPACE/" ./rbac/deployment.yaml
kubectl -n $NAMESPACE apply -f ./rbac
```
