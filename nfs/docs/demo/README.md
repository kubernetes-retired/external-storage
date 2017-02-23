#Demo

The [beta dynamic provisioning feature](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html) allows administrators to define `StorageClasses` to enable Kubernetes to create `PersistentVolumes` on-demand. Kubernetes includes many [provisioners](http://kubernetes.io/docs/user-guide/persistent-volumes/#provisioner) to specify in `StorageClasses` definitions and now, with Kubernetes 1.5, also includes support for [external or out-of-tree provisioners](https://github.com/kubernetes/kubernetes/pull/30285) like [nfs-provisioner](https://github.com/kubernetes-incubator/external-storage/nfs).

nfs-provisioner creates NFS-backed PV's, leveraging the NFS volume plugin of Kubernetes, so given the ubiquity of NFS it will work almost anywhere. It's ideal for local clusters and dev work, any place a PV is wanted but not the manual work of creating one. We'll demonstrate how to get it quickly up and running, following a variation of the Kubernetes repo's [NFS example](https://github.com/kubernetes/kubernetes/tree/release-1.5/examples/volumes/nfs).

If the cluster you intend to follow this demo with has RBAC and/or PSP enabled or it's an OpenShift cluster, you must first complete the [authorization guide](../authorization.md).

The recommended way to run nfs-provisioner, which we'll demonstrate here, is as a [single-instance stateful app](http://kubernetes.io/docs/tutorials/stateful-application/run-stateful-application/), where we create a `Deployment` and back it with some persistent storage like a `hostPath` volume. We always create it in tandem with a matching service that has the necessary ports exposed. We'll see that when it's setup like so, the NFS server it runs to serve its PV's can maintain state and so survive pod restarts. The other ways to run are as a `DaemonSet`, standalone Docker container, or standalone binary, all documented [here](../deployment.md)

There are two main things one can customize here before creating the deployment: the provisioner name and the backing volume.

The provisioner name must follow the naming scheme `<vendor name>/<provisioner name>`, like for example `kubernetes.io/gce-pd`. It's specified here in the `args` field. This is the `provisioner` a `StorageClass` will specify later. We'll use the name `example.com/nfs-tmp`.

```yaml
...
args:
  - "-provisioner=example.com/nfs"
...
```

The backing volume is the place mounted at `/export` where the nfs-provisioner instance stores its state and the data of every PV it provisions. So we can mount any volume there to specify that volume as the backing storage for provisioned PV's. We'll use a [`hostPath`](http://kubernetes.io/docs/user-guide/volumes/#hostpath) volume at `/tmp/nfs-provisioner`, so we need to make sure that the directory exists on the nodes our deployment's pod could be scheduled to and, if selinux is enforcing, that it is labelled appropriately.

```yaml
...
  volumeMounts:
    - name: export-volume
      mountPath: /export
volumes:
  - name: export-volume
    hostPath:
      path: /tmp/nfs-provisioner
...
```

```console
$ mkdir -p /tmp/nfs-provisioner
$ sudo chcon -Rt svirt_sandbox_file_t /tmp/nfs-provisioner
```

If you completed the [authorization guide](../authorization.md) (because your cluster has RBAC and/or PSP enabled or it's an OpenShift cluster) and it told you to remember to ensure the pod template of the deployment specifies the service account you created, do that now as well by adding a `serviceAccount` line.
```yaml
...
    spec:
      serviceAccount: nfs-provisioner
      containers:
...
```

We create the deployment and its service.

```console
$ kubectl create -f deployment.yaml
service "nfs-provisioner" created
deployment "nfs-provisioner" created
```

Now, our instance of nfs-provisioner can be treated like any other provisioner: we specify its name in a `StorageClass` object and the provisioner will automatically create `PersistentVolumes` for `PersistentVolumeClaims` that ask for the `StorageClass`. We'll show all that.

We create a `StorageClass` that specifies our provisioner.

```console
$ kubectl create -f class.yaml
storageclass "example-nfs" created
```

We create a `PersistentVolumeClaim` asking for our `StorageClass`.

```console
$ kubectl create -f claim.yaml
persistentvolumeclaim "nfs" created
```

And a `PersistentVolume` is provisioned automatically and already bound to our claim. We didn't have to manually figure out the NFS server's IP, put that IP into a PV yaml, then create the yaml. We just had to deploy our nfs-provisioner and create a `StorageClass` for it, which are one-time steps.

```console
$ kubectl get pv
NAME                                       CAPACITY   ACCESSMODES   RECLAIMPOLICY   STATUS      CLAIM         REASON    AGE
pvc-dce84888-7a9d-11e6-b1ee-5254001e0c1b   1Mi        RWX           Delete          Bound       default/nfs        
```

If you don't see a PV bound to your PVC, check the deployment's provisioner pod's logs using `kubectl logs` and look for events in the PVC using `kubectl describe`.

Now we have an NFS-backed PVC & PV pair that is exactly like what is expected by the official Kubernetes NFS example, so we'll finish the [example](https://github.com/kubernetes/kubernetes/tree/release-1.5/examples/volumes/nfs#setup-the-fake-backend) to show our storage works, can be shared, and persists. If you don't need that proof, you can skip ahead to the part where we discuss deleting and cleaning up the provisioner and its storage.

We setup the fake backend that updates `index.html` on the NFS server every 10 seconds. And check that our mounts are working.

```console
$ kubectl create -f nfs-busybox-rc.yaml
$ kubectl get pod -l name=nfs-busybox
NAME                READY     STATUS    RESTARTS   AGE
nfs-busybox-h782l   1/1       Running   0          13m
nfs-busybox-nul47   1/1       Running   0          13m
$ kubectl exec nfs-busybox-h782l -- cat /mnt/index.html
Mon Dec 19 18:10:09 UTC 2016
nfs-busybox-h782l
```

We setup the web server that reads from the NFS share and runs a simple web server on it. And check that `nginx` is serving the data, the `index.html` from above, appropriately.

```console
$ kubectl create -f nfs-web-rc.yaml
$ kubectl create -f nfs-web-service.yaml
$ kubectl get pod -l name=nfs-busybox
NAME                READY     STATUS    RESTARTS   AGE
nfs-busybox-h782l   1/1       Running   0          13m
nfs-busybox-nul47   1/1       Running   0          13m
$ kubectl get services nfs-web
NAME      CLUSTER-IP   EXTERNAL-IP   PORT(S)   AGE
nfs-web   10.0.0.187   <none>        80/TCP    7s
$ kubectl exec nfs-busybox-h782l -- wget -qO- http://10.0.0.187
Mon Dec 19 18:11:51 UTC 2016
nfs-busybox-nul47
```

We see that the PV created by our nfs-provisioner works, let's now show that it will continue to work even after our nfs-provisioner pod restarts. Because of how NFS works, anything that has shares mounted will hang as it tries to access or unmount them while the NFS server is down. Recall that all our nfs-provisioner instance's state and data persists in the volume we mounted at `/export`, so it should recover and its shares become accessible again when it, and the NFS server it runs, restarts. We'll simulate this situation.

We scale the deployment down to 0 replicas.

```console
$ kubectl scale --replicas=0 deployment/nfs-provisioner
deployment "nfs-provisioner" scaled
```

We try the same check from before that `nginx` is serving the data, and we see it hangs indefinitely as it tries to read the share.

```console
$ kubectl exec nfs-busybox-h782l -- wget -qO- http://10.0.0.187
...
^C
```

We scale the deployment back up to 1 replica.

```console
$ kubectl scale --replicas=1 deployment/nfs-provisioner
deployment "nfs-provisioner" scaled
```

And after a brief delay all should be working again.

```console
$ kubectl exec nfs-busybox-h782l -- wget -qO- http://10.0.0.187
Mon Dec 19 18:21:49 UTC 2016
nfs-busybox-nul47
```

Now we'll show how to delete the storage provisioned by our nfs-provisioner once we're done with it. Let's first delete the fake backend and web server that are  using the PVC.

```console
$ kubectl delete rc nfs-busybox nfs-web
replicationcontroller "nfs-busybox" deleted
replicationcontroller "nfs-web" deleted
$ kubectl delete service nfs-web
service "nfs-web" deleted
```

Once all those pods have disappeared and so we are confident they have unmounted the NFS share, we can safely delete the PVC. The provisioned PV the PVC is bound to has the `ReclaimPolicy` `Delete`, so when we delete the PVC, the PV and its data will be automatically deleted by our nfs-provisioner.

```console
$ kubectl delete pvc nfs
persistentvolumeclaim "nfs" deleted
$ kubectl get pv
```

Note that deleting an nfs-provisioner instance won't delete the PV's it created, so before we do so we need to make sure none still exist as they would be useless for as long as the provisioner is gone.

```console
$ kubectl delete deployment nfs-provisioner
deployment "nfs-provisioner" deleted
$ kubectl delete service nfs-provisioner
service "nfs-provisioner" deleted
```

Thanks for following along. If at any point things didn't work correctly, check the provisioner pod's logs using `kubectl logs` and look for events in the PV's and PVC's using `kubectl describe`. If you are interested in Kubernetes storage-related things like this, head to the [Storage SIG](http://blog.kubernetes.io/2016/10/dynamic-provisioning-and-storage-in-kubernetes.html). If you are interested in writing your own external provisioner, all the code is available for you to read or fork, and better documentation on how to do it is in the works.

