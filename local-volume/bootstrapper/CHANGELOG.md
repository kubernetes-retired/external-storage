# v1.0.1
* Launches provisioner as privilged pods

# v1.0.0
* By default, creates a service account and cluster role bindings for the local storage provisioner
* Allow user-configurable service account, volume configmap, and provisioner image
* Launches the provisioner DaemonSet based on the service account and volume config
