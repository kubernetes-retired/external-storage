# install iscsi controller

```
export NS=default
kubectl apply -f iscsi-provisioner-class.yaml 
kubectl create secret generic targetd-account --from-literal=username=admin --from-literal=password=ciao -n $NS
kubectl apply -f iscsi-provisioner-d.yaml -n $NS
kubectl apply -f iscsi-provisioner-pvc.yaml -n $NS
``` 