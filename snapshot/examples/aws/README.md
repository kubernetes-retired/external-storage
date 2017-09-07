### AWS EBS

[![asciicast](https://asciinema.org/a/5jfggavfbkayuf7lkpe6n7li1.png)](https://asciinema.org/a/5jfggavfbkayuf7lkpe6n7li1)

#### Start Snapshot Controller 

(assuming running Kubernetes local cluster):
```
_output/bin/snapshot-controller  -kubeconfig=${HOME}/.kube/config -cloudprovider=aws
```

####  Create a snapshot
 * Create an PVC
```bash
kubectl create namespace myns
# if no default storage class, create one
kubectl create -f https://raw.githubusercontent.com/kubernetes/kubernetes/master/examples/persistent-volume-provisioning/aws-ebs.yaml
kubectl create -f examples/aws/pvc.yaml
```
 * Create a Snapshot Third Party Resource 
```bash
kubectl create -f examples/aws/snapshot.yaml
```

#### Check VolumeSnapshot and VolumeSnapshotData are created

```bash
kubectl get volumesnapshot,volumesnapshotdata -o yaml --namespace=myns
```

## Snapshot based PV Provisioner

Unlike exiting PV provisioners that provision blank volume, Snapshot based PV provisioners create volumes based on existing snapshots. Thus new provisioners are needed.

There is a special annotation give to PVCs that request snapshot based PVs. As illustrated in [the example](examples/aws/claim.yaml), `snapshot.alpha.kubernetes.io` must point to an existing VolumeSnapshot Object
```yaml
metadata:
  name: 
  namespace: 
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
```
### Starting Snapshot based PV Provisioner

```bash
_output/bin/snapshot-provisioner  -kubeconfig=${HOME}/.kube/config -cloudprovider=aws
```

### Create Storage Class to enable provisioner PVs based on volume snapshots

```bash
kubectl create -f examples/aws/class.yaml
```

### Claims a PV that restores from an existing volume snapshot

```bash
kubectl create -f examples/aws/claim.yaml
```
