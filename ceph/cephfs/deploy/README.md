# Deployment

## Table of contents

* [Install without RBAC roles](#install-without-rbac-roles)
* [Install with RBAC roles](#install-with-rbac-roles)

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
kubectl -n $NAMESPACE apply -f ./rbac
```
