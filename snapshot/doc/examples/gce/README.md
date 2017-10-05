## GCE PD


#### Start Snapshot Controller 

(assuming running inside of Kubernetes cluster master node):
```
_output/bin/snapshot-controller  -kubeconfig=${HOME}/.kube/config -cloudprovider=gce
```

####  Create a snapshot
 * Create a PVC
```bash
kubectl create namespace myns
# if no default storage class, create Provisioner
kubectl create -f examples/gce/class.yaml

kubectl -f examples/gce/pvc.yaml
```
 * Create a Snapshot Third Party Resource 
```bash
kubectl -f examples/gce/snapshot.yaml
```

#### Check VolumeSnapshot and VolumeSnapshotData are created

```bash
kubectl get volumesnapshot,volumesnapshotdata -o yaml --namespace=myns
```

## Snapshot based PV Provisioner

Unlike existing PV provisioners that provision blank volume, Snapshot based PV provisioners create volumes based on existing snapshots. Thus new provisioners are needed.

There is a special annotation give to PVCs that request snapshot based PVs. As illustrated in [the example](./claim.yaml), `snapshot.alpha.kubernetes.io` must point to an existing VolumeSnapshot Object
```yaml
metadata:
  name: 
  namespace: 
  annotations:
    snapshot.alpha.kubernetes.io/snapshot: snapshot-demo
```

## GCE PD Volume Type

#### Start PV Provisioner and Storage Class to restore a snapshot to a PV

Start provisioner (assuming running Kubernetes local cluster):
```bash
_output/bin/snapshot-provisioner  -kubeconfig=${HOME}/.kube/config -cloudprovider=gce
```

Create a storage class:
```bash
kubectl create -f examples/gce/provision.yaml
```

### Create a PVC that claims a PV based on an existing snapshot 

```bash
kubectl create -f examples/gce/claim.yaml
```

#### Check PV and PVC are created

```bash
kubectl get pv,pvc
```
  
