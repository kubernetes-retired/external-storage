## Running Multiple Provisioners

### Single StorageClass

Multiple nfs-provisioner instances can have the same name, i.e. the same value for the `provisioner` argument. They will watch for the same class of claims. When a claim is added, they will race to acquire a lock on it, and only the winner may actually attempt to provision a volume while the others must wait for success or failure. By default, the winner has up to 30 seconds to succeed or fail to provision a volume, after which the other provisioners again race for the lock. This minimizes the number of calls to `Provision`.

### Multiple StorageClasses

Multiple nfs-provisioner with different names can be running at the same time. They won't conflict because they'll try to provision storage for their own classes of claims.

### Scaling

Given that multiple instances can have the same name, to scale up or down a set of provisioner pods, you simply create or delete pods with the same provisioner name. This can mean adding nodes/pods to a DaemonSet or creating more Deployments, as described in the [Deployment](deployment.md) doc. Each additional instance would back its PVs with different storage, effectively creating & adding to a "pool" of storage.
