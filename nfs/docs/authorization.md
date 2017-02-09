# Authorization

If you chose to run the provisioner in Kubernetes you may need to grant it authorization to make the API requests and syscalls it needs to. Creating `PersistentVolumes` is normally an administrator's responsibility and the authorization policies of Kubernetes & OpenShift will by default deny a pod the authorization to make such API requests and syscalls. A Kubernetes RBAC API request denial looks like this:

>E0124 20:10:01.475115       1 reflector.go:199] github.com/kubernetes-incubator/nfs-provisioner/vendor/k8s.io/client-go/tools/cache/reflector.go:94: Failed to list *v1beta1.StorageClass: the server does not allow access to the requested resource (get storageclasses.storage.k8s.io)

Find out what authorization plugin or policy implementation your cluster uses, if any, and follow one of the below sections.

* [PSP and/or RBAC](#rbac)
* [OpenShift](#openshift)

## PSP and/or RBAC

Your cluster may have [PSPs](https://kubernetes.io/docs/user-guide/pod-security-policy/) (Pod Security Policies) and/or [RBAC](https://kubernetes.io/docs/admin/authorization/) (Role-Based Access Control) enabled. You should probably [take advantage of both](https://github.com/kubernetes/kubernetes/tree/release-1.5/examples/podsecuritypolicy/rbac) if you want to use one or the other at all but if your cluster has only one enabled:
* PSP: you need only create the PSP

	```console
	$ kubectl create -f deploy/kubernetes/auth/psp.yaml
	podsecuritypolicy "nfs-provisioner" created
	```
* RBAC: ignore the step where you create the PSP, the `ClusterRole` will still work even if the PSP doesn't exist

RBAC doesn't have a bootstrap `ClusterRole` with the permissions nfs-provisioner needs so you need to create a `ClusterRole` that lists the permissions plus a `ClusterRoleBinding` that grants the permissions to the service account the nfs-provisioner pod will be assigned.

Create the service account. Later, you will have to ensure the pod template of the deployment/statefulset/daemonset specifies this service account.

```console
$ kubectl create -f deploy/kubernetes/auth/serviceaccount.yaml
serviceaccounts/nfs-provisioner
```

Create the PSP.

```console
$ kubectl create -f deploy/kubernetes/auth/psp.yaml
serviceaccounts/nfs-provisioner
```

`deploy/kubernetes/auth/clusterrole.yaml` lists all the permissions nfs-provisioner needs.

Create the `ClusterRole`.

```console
$ kubectl create -f deploy/kubernetes/auth/clusterrole.yaml
clusterrole "nfs-provisioner-runner" created
```

`deploy/kubernetes/auth/clusterrolebinding.yaml` binds the "nfs-provisioner" service account in namespace `default` to your `ClusterRole`. Edit the service account name and namespace accordingly if you are not in the namespace `default` or named the service account something other than "nfs-provisioner".

Create the `ClusterRoleBinding`.
```console
$ kubectl create -f deploy/kubernetes/auth/clusterrolebinding.yaml
clusterrolebinding "run-nfs-provisioner" created
```

Remember: later, you will have to ensure the pod template of the deployment/statefulset/daemonset specifies the service account you created.

## OpenShift

OpenShift by default has both [authorization policies](https://docs.openshift.com/container-platform/latest/admin_guide/manage_authorization_policy.html) and [security context constraints](https://docs.openshift.com/container-platform/latest/admin_guide/manage_scc.html) that deny an nfs-provisioner pod its needed permissions, so you need to create a new `ClusterRole` and SCC for your pod to use.

Create the service account. Later, you will have to ensure the pod template of the deployment/statefulset/daemonset specifies this service account.

```
$ oc create -f deploy/kubernetes/auth/serviceaccount.yaml
serviceaccount "nfs-provisioner" created
```

`deploy/kubernetes/auth/openshift-scc.yaml` defines an SCC for your nfs-provisioner pod to validate against.

Create the SCC.

```console
$ oc create -f deploy/kubernetes/auth/openshift-scc.yaml
securitycontextconstraints "nfs-provisioner" created
```

Add the `nfs-provisioner` service account to the SCC. Change the service account name and namespace accordingly if you are not in the namespace `default` or named the service account something other than "nfs-provisioner".

```console
$ oadm policy add-scc-to-user nfs-provisioner system:serviceaccount:default:nfs-provisioner
```

`deploy/kubernetes/auth/openshift-clusterrole.yaml` lists all the permissions nfs-provisioner needs.

Create the `ClusterRole`.

```console
$ oc create -f deploy/kubernetes/auth/openshift-clusterrole.yaml
clusterrole "nfs-provisioner-runner" created
```

Add the `ClusterRole` to the `nfs-provisioner` service account. Change the service account name and namespace accordingly if you are not in the namespace `default` or named the service account something other than "nfs-provisioner".

```console
$ oadm policy add-cluster-role-to-user nfs-provisioner-runner system:serviceaccount:default:nfs-provisioner
```

Remember: later, you will have to ensure the pod template of the deployment/statefulset/daemonset specifies the service account you created.

---

Now that you have finished authorizing the provisioner, go to [Deployment](deployment.md) for info on how to deploy it.
