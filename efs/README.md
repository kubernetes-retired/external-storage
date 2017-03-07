# efs-provisioner

[![Docker Repository on Quay](https://quay.io/repository/external_storage/efs-provisioner/status "Docker Repository on Quay")](https://quay.io/repository/external_storage/efs-provisioner)
```
quay.io/external_storage/efs-provisioner:latest
```

## Prerequisites
* An IAM user assigned the AmazonElasticFileSystemReadOnlyAccess policy (or better)
* An EFS file system in your cluster's region
* [Mount targets](http://docs.aws.amazon.com/efs/latest/ug/accessing-fs.html) and [security groups](http://docs.aws.amazon.com/efs/latest/ug/accessing-fs-create-security-groups.html) such that any node (in any zone in the cluster's region) can mount the EFS file system by its [File system DNS name](http://docs.aws.amazon.com/efs/latest/ug/mounting-fs-mount-cmd-dns-name.html)

## Deployment

Create a configmap containing the [**File system ID**](http://docs.aws.amazon.com/efs/latest/ug/gs-step-two-create-efs-resources.html) and Amazon EC2 region of the EFS file system you wish to provision NFS PVs from, plus the name of the provisioner, which administrators will specify in the `provisioner` field of their `StorageClass(es)`, e.g. `provisioner: example.com/aws-efs`.

```console
$ kubectl create configmap efs-provisioner \
--from-literal=file.system.id=fs-47a2c22e \
--from-literal=aws.region=us-west-2 \
--from-literal=provisioner.name=example.com/aws-efs
```

Create a secret containing the AWS credentials of a user assigned the AmazonElasticFileSystemReadOnlyAccess policy. The credentials will be used by the provisioner only once at startup to check that the EFS file system you specified in the configmap actually exists.

```console
$ kubectl create secret generic aws-credentials \
--from-literal=aws-access-key-id=AKIAIOSFODNN7EXAMPLE \
--from-literal=aws-secret-access-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

Decide on & set aside a directory within the EFS file system for the provisioner to use. The provisioner will create child directories to back each PV it provisions. Then edit the `volumes` section at the bottom of "deploy/deployment.yaml" so that the `path` refers to the directory you set aside and the `server` is the same EFS file system you specified. Create the deployment, and you're done.

```yaml
      volumes:
        - name: pv-volume
          nfs:
            server: fs-47a2c22e.efs.us-west-2.amazonaws.com
            path: /persistentvolumes
```

```console
$ kubectl create -f deploy/deployment.yaml
deployment "efs-provisioner" created
```

### Authorization

If your cluster has RBAC enabled or you are running OpenShift you must authorize the provisioner. If you are in a namespace/project other than "default" either edit `deploy/auth/clusterrolebinding.yaml` or edit the `oadm policy` command accordingly.

#### RBAC
```console
$ kubectl create -f deploy/auth/serviceaccount.yaml
serviceaccount "efs-provisioner" created
$ kubectl create -f deploy/auth/clusterrole.yaml
clusterrole "efs-provisioner-runner" created
$ kubectl create -f deploy/auth/clusterrolebinding.yaml
clusterrolebinding "run-efs-provisioner" created
$ kubectl patch deployment efs-provisioner -p '{"spec":{"template":{"spec":{"serviceAccount":"efs-provisioner"}}}}'
```

#### OpenShift
```console
$ oc create -f deploy/auth/serviceaccount.yaml
serviceaccount "efs-provisioner" created
$ oc create -f deploy/auth/openshift-clusterrole.yaml
clusterrole "efs-provisioner-runner" created
$ oadm policy add-cluster-role-to-user efs-provisioner-runner system:serviceaccount:default:efs-provisioner created
$ oc patch deployment efs-provisioner -p '{"spec":{"template":{"spec":{"serviceAccount":"efs-provisioner"}}}}'
```
### SELinux
If SELinux is enforcing on the node where the provisioner runs, you must enable writing from a pod to a remote NFS server (EFS in this case) on the node by running:
```console
$ setsebool -P virt_use_nfs 1
$ setsebool -P virt_sandbox_use_nfs 1
```
https://docs.openshift.org/latest/install_config/persistent_storage/persistent_storage_nfs.html#nfs-selinux

## Usage

First a [`StorageClass`](https://kubernetes.io/docs/user-guide/persistent-volumes/#storageclasses) for claims to ask for needs to be created.

```yaml
apiVersion: storage.k8s.io/v1beta1
kind: StorageClass
metadata:
  name: slow
provisioner: example.com/aws-efs
parameters:
  gidMin: "40000"
  gidMax: "50000"
```

### Parameters

* `gidMin` + `gidMax` : The minimum and maximum value of GID range for the storage class. A unique value (GID) in this range ( gidMin-gidMax ) will be used for dynamically provisioned volumes. These are optional values. If not specified, the volume will be provisioned with a value between 2000-2147483647 which are defaults for gidMin and gidMax respectively.

Once you have finished configuring the class to have the name you chose when deploying the provisioner and the parameters you want, create it.

```console
$ kubectl create -f deploy/class.yaml 
storageclass "aws-efs" created
```

When you create a claim that asks for the class, a volume will be automatically created.

```console
$ kubectl create -f deploy/claim.yaml 
persistentvolumeclaim "efs" created
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS    CLAIM         REASON    AGE
pvc-557b4436-ed73-11e6-84b3-06a700dda5f5   1Mi        RWX           Delete          Bound     default/efs             2s
```
Note: any pod that consumes the claim will be able to read/write to the volume. This is because the volumes are provisioned with a GID (from the default range or according to `gidMin` + `gidMax`) and any pod that mounts the volume via the claim automatically gets the GID as a supplemental group.
