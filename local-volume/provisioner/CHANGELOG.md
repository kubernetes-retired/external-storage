# v1.0.1
* Change fs capacity detection to use K8s volume util method
* Add event on PV if cleanup or deletion fails

# v1.0.0
* Run a provisioner on each node via DaemonSet
* Discovers file-based volumes under configurable discovery directories and creates a local PV for each
* When PV created by the provisioner is released, delete file contents and delete PV, to be discovered again.
* Use PV informer to populate volume cache
