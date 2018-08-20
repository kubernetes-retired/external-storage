# [v2.2.0](https://github.com/kubernetes-incubator/external-storage/releases/tag/local-volume-provisioner-v2.2.0)
Image updates:
* Add Prometheus metrics
  ([#721](https://github.com/kubernetes-incubator/external-storage/pull/721))
* Support Retain reclaim policy
  ([#776](https://github.com/kubernetes-incubator/external-storage/pull/776))
* Add option for resync period and add a default of 5 minutes
  ([#800](https://github.com/kubernetes-incubator/external-storage/pull/800))
* Add option for cleaning filesystem PVs in a Job
  ([#863](https://github.com/kubernetes-incubator/external-storage/pull/863))
* Add option for using only Node.Name as the provisioner name, instead of Node.UID ([#947](https://github.com/kubernetes-incubator/external-storage/pull/947))

Deployment updates:
* Refactor helm generation
  ([#789](https://github.com/kubernetes-incubator/external-storage/pull/789))
* Add option for tolerations
  ([#792](https://github.com/kubernetes-incubator/external-storage/pull/792))
* Add /dev volume mount for raw block support
  ([#799)](https://github.com/kubernetes-incubator/external-storage/pull/799)
* Add option for resource requests and limits
  ([#831](https://github.com/kubernetes-incubator/external-storage/pull/831))

# [v2.1.0](https://github.com/kubernetes-incubator/external-storage/releases/tag/local-volume-provisioner-v2.1.0)
The following changes require Kubernetes 1.10 or higher.
* Add block volumeMode discovery and cleanup.
* **Important:** Beta PV.NodeAffinity field is used by default. If running against an older K8s version,
  the `useAlphaAPI` flag must be set in the configMap.

# [v2.0.0](https://github.com/kubernetes-incubator/external-storage/releases/tag/local-volume-provisioner-v2.0.0)
**Important:** This version is incompatible and has breaking changes with v1!
* Remove default config, a configmap is now required.
* Configmap data is changed from json to yaml syntax.
* All local volumes must be mount points.  For directory-based volumes, a
  bind-mount must be done in order for the provisioner to discover them. This
  requires the K8s [mount propagation feature](https://kubernetes.io/docs/concepts/storage/volumes/#mount-propagation)
  to be enabled.
* Detected capacity is rounded down to the nearest GB.
* New option to specify which node labels to add to the PV.

# [v1.0.1](https://github.com/kubernetes-incubator/external-storage/releases/tag/local-volume-provisioner-bootstrap-v1.0.1)
* Change fs capacity detection to use K8s volume util method.
* Add event on PV if cleanup or deletion fails.

# [v1.0.0](https://github.com/kubernetes-incubator/external-storage/releases/tag/local-volume-provisioner-bootstrap-v1.0.0)
* Run a provisioner on each node via DaemonSet.
* Discovers file-based volumes under configurable discovery directories and creates a local PV for each.
* When PV created by the provisioner is released, delete file contents and delete PV, to be discovered again.
* Use PV informer to populate volume cache.
