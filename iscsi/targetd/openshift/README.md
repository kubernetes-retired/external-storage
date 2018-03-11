# install iscsi controller

```
oc new-project iscsi-provisioner
oc create sa iscsi-provisioner
# if Openshift is version < 3.6 add the iscsi-provisioner-runner role
oc create -f iscsi-auth.yaml
# else if Openshift is version >= 3.6 add the system:persistent-volume-provisioner role
oc adm policy add-cluster-role-to-user system:persistent-volume-provisioner system:serviceaccount:iscsi-provisioner:iscsi-provisioner
#
oc create -f iscsi-provisioner-class.yaml 
oc secret new-basicauth targetd-account --username=admin --password=ciao
oc create -f iscsi-provisioner-dc.yaml
oc create -f iscsi-provisioner-pvc.yaml
```
