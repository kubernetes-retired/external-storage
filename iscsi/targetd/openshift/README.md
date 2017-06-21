# install iscsi controller

```
oc new-project iscsi-provisioner
oc create sa iscsi-provisioner
oc adm policy add-cluster-role-to-user cluster-reader system:serviceaccount:iscsi-provisioner:iscsi-provisioner
oc adm policy add-cluster-role-to-user system:pv-provisioner-controller system:serviceaccount:iscsi-provisioner:iscsi-provisioner
oc adm policy add-cluster-role-to-user system:pv-binder-controller system:serviceaccount:iscsi-provisioner:iscsi-provisioner
oc adm policy add-cluster-role-to-user system:pv-recycler-controller system:serviceaccount:iscsi-provisioner:iscsi-provisioner
oc create -f iscsi-provisioner-class.yaml 
oc secret new-basicauth targetd-account --username=admin --password=ciao
oc create -f iscsi-provisioner-dc.yaml
oc create -f iscsi-provisioner-pvc.yaml
``` 