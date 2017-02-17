# Authorization
The controller requires authorization to perform the following API calls:
* `get`, `list`, `watch`, `create`, `delete` "persistentvolumes"
* `get`, `list`, `watch`, `update` "persistentvolumeclaims"
* `get`, `list`, `watch` "storageclasses"
* `watch`, `create`, `update`, `patch` "events"

As of Kubernetes 1.6 these needed permissions are enumerated in an RBAC bootstrap `ClusterRole` named ["system:persistent-volume-provisioner"](https://github.com/kubernetes/kubernetes/blob/4e01d1d1412950250148d25ca607fb9585f4c86b/plugin/pkg/auth/authorizer/rbac/bootstrappolicy/testdata/cluster-roles.yaml#L693). In OpenShift this bootstrap `ClusterRole` doesn't yet exist but it would look exactly the same except for the `apiVersion` field.

As the author of your external provisioner you will need to instruct users on how to authorize the provisioner. Assuming you intend for the provisioner to be deployed as an application on top of Kubernetes/OpenShift, authorization means creating a service account for the provisioner to run as and granting the service account the needed permissions.

In Kubernetes you grant the needed permissions by creating a `ClusterRoleBinding` that refers to "system:persistent-volume-provisioner".
In OpenShift you do so by running something like: `oadm policy add-cluster-role-to-user system:persistent-volume-provisioner system:serviceaccount:default:my-provisioner`
