## Running Multiple Provisioners

### Single StorageClass

Multiple nfs-provisioner instances can have the same name, i.e. the same value for the `provisioner` argument. They will all attempt to provision storage for the same class of claims. Only one will successfully create a `PersistentVolume.` The others will fail and eventually move on.

### Multiple StorageClasses

Multiple nfs-provisioner with different names can be running at the same time. They won't conflict because they'll try to provision storage for their own classes of claims.

### Scaling

Given that multiple instances can have the same name, to scale up or down a set of provisioner pods (or pairs of deployments & services), you simply create or delete pods (or deployments & services) with the same provisioner name. 
