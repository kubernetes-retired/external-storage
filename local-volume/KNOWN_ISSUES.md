# Known Issues and Limitations

## K8s

### 1.9: Alpha

* If you prebind a PVC (by setting PVC.VolumeName) at the same time that another
Pod is being scheduled, it's possible that the Pod's PVCs will encounter a partial
binding failure.  Manual recovery is needed in this situation.
    * Workarounds:
         * Don't prebind PVCs and have Kubernetes bind volumes for the same
           StorageClass.
         * Prebind PV upon creation instead.

### 1.7: Alpha

* Multiple local PVCs in a single pod.
    * Fixed in 1.9.
    * No known workarounds.
* PVC binding does not consider pod scheduling requirements and may make
  suboptimal or incorrect decisions.
    * Fixed in 1.9.
    * Workarounds:
        * Run your pods that require local storage first.
        * Give your pods high priority.
        * Run a workaround controller that unbinds PVCs for pods that are
          stuck pending.

## Provisioner

### 1.0:
* The provisioner will not correctly detect new mounts added after it has been started.
  The local PV capacity will be reported as the root filesystem capacity.
    * Fixed with provisioner 2.0 + K8s 1.8. Requires mount propagation alpha
      feature.
    * Workaround:
        * Before adding any new mount points, stop the provisioner daemonset, add the
          new mount points, start the daemonset.
