# local-volume-update-pv-to-beta

local-volume-update-pv-to-beta is used to update local PVs alpha node affinity annotation to beta
Below is how to compile and use the tool.

## Deployment

### Compile the tool
``` console
make
```

### Make the container image and push to the registry
``` console
make push
```

### Clean the binary
``` console
make clean
```

### Create local PV with alpha node affinity
below is an example of local PV
``` pv.yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: example-local-pv-1
  annotations:
    "volume.alpha.kubernetes.io/node-affinity": '{
      "requiredDuringSchedulingIgnoredDuringExecution": {
        "nodeSelectorTerms": [
          { "matchExpressions": [
            { "key": "kubernetes.io/hostname",
              "operator": "In",
              "values": ["nickren-14"]
            }
          ]}
         ]}
        }'
spec:
  capacity:
    storage: 200Mi
  accessModes:
  - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: local-storage
  local:
    path: /mnt/disks/vol/vol1
```
create PV and describe it
``` kubectl describe pv example-local-pv-1
Name:            example-local-pv-1
Labels:          <none>
Annotations:     volume.alpha.kubernetes.io/node-affinity={ "requiredDuringSchedulingIgnoredDuringExecution": { "nodeSelectorTerms": [ { "matchExpressions": [ { "key": "kubernetes.io/hostname", "operator": "In", "valu...
Finalizers:      [kubernetes.io/pv-protection]
StorageClass:    local-storage
Status:          Available
Claim:
Reclaim Policy:  Retain
Access Modes:    RWO
Capacity:        200Mi
Node Affinity:   <none>
Message:
Source:
    Type:  LocalVolume (a persistent volume backed by local storage on a node)
    Path:  /mnt/disks/vol/vol1
Events:    <none>
```

### Create ServiceAccount and kubernetes job to update local PV alpha node affinity to beta
``` console
kubectl create -f deployment/kubernetes/admin-account.yaml
kubectl create -f deployment/kubernetes/update-pv-to-beta.yaml
```

### Describe the kubernetes job to see if it succeeds
``` kubectl get job
kubectl get job
NAME                   DESIRED   SUCCESSFUL   AGE
local-volume-updater   1         1            10s
```
``` kubectl describe job local-volume-updater
kubectl describe job local-volume-updater
Name:           local-volume-updater
Namespace:      default
Selector:       controller-uid=c2a02fe4-3641-11e8-afd6-080027765304
Labels:         app=local-volume-updater
Annotations:    <none>
Parallelism:    1
Completions:    1
Start Time:     Mon, 02 Apr 2018 14:47:50 +0800
Pods Statuses:  0 Running / 1 Succeeded / 0 Failed
Pod Template:
  Labels:           controller-uid=c2a02fe4-3641-11e8-afd6-080027765304
                    job-name=local-volume-updater
  Service Account:  local-storage-update
  Containers:
   updater:
    Image:        quay.io/external_storage/local-volume-update-pv-to-beta:latest
    Port:         <none>
    Host Port:    <none>
    Environment:  <none>
    Mounts:       <none>
  Volumes:        <none>
Events:
  Type    Reason            Age   From            Message
  ----    ------            ----  ----            -------
  Normal  SuccessfulCreate  20s   job-controller  Created pod: local-volume-updater-lb5zs
```
if error occurs, we can use `kubectl get pods` and `kubectl logs $podID` to see the error log and debug

### Describe the local PV again to check if the alpha node affinity is updated
``` kubectl describe pv example-local-pv-1
Name:              example-local-pv-1
Labels:            <none>
Annotations:       <none>
Finalizers:        [kubernetes.io/pv-protection]
StorageClass:      local-storage
Status:            Available
Claim:
Reclaim Policy:    Retain
Access Modes:      RWO
Capacity:          200Mi
Node Affinity:
  Required Terms:
    Term 0:        kubernetes.io/hostname in [nickren-14]
Message:
Source:
    Type:  LocalVolume (a persistent volume backed by local storage on a node)
    Path:  /mnt/disks/vol/vol1
Events:    <none>
```

### Delete ServiceAccount and kubernetes job
``` console
kubectl delete -f deployment/kubernetes/admin-account.yaml
kubectl delete -f deployment/kubernetes/update-pv-to-beta.yaml
```
