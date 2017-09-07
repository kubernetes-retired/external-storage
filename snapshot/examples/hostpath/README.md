## Quick Howto


#### Start Snapshot Controller 

(assuming running Kubernetes local cluster):
```
_output/bin/snapshot-controller  -kubeconfig=${HOME}/.kube/config
```

####  Create a snapshot
 * Create a hostpath PV and PVC
```bash
kubectl create namespace myns
kubectl -f examples/hostpath/pv.yaml
kubectl -f examples/hostpath/pvc.yaml
```
 * Create a Snapshot Third Party Resource 
```bash
kubectl -f examples/hostpath/snapshot.yaml
```

#### Check VolumeSnapshot and VolumeSnapshotData are created

```bash
kubectl get volumesnapshot,volumesnapshotdata -o yaml --namespace=myns
```

## Snapshot based PV Provisioner

Unlike exiting PV provisioners that provision blank volume, Snapshot based PV provisioners create volumes based on existing snapshots. Thus new provisioners are needed.

There is a special annotation give to PVCs that request snapshot based PVs. As illustrated in [the example](examples/hostpath/claim.yaml), `snapshot.alpha.kubernetes.io` must point to an existing VolumeSnapshot Object
```yaml
metadata:
  name: 
  namespace: 
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
```

## HostPath Volume Type

#### Start PV Provisioner and Storage Class to restore a snapshot to a PV

Start provisioner (assuming running Kubernetes local cluster):
```bash
_output/bin/snapshot-provisioner  -kubeconfig=${HOME}/.kube/config
```

Create a storage class:
```bash
kubectl create -f examples/hostpath/class.yaml
```

### Create a PVC that claims a PV based on an existing snapshot 

```bash
kubectl create -f examples/hostpath/claim.yaml
```

#### Check PV and PVC are created

```bash
kubectl get pv,pvc
```
  
