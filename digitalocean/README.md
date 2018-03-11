# Kubernetes DigitalOcean provisioner

This is an simple provisioner for DigitalOcean [Block Storage](https://www.digitalocean.com/products/storage/).

**Deploy**

1. Get a DigitalOcean access-token [here](https://cloud.digitalocean.com/settings/api/tokens).  
   `base64` the token and insert it into [manifests/digitalocean-secret.yaml](manifests/digitalocean-secret.yaml).  
   Create the secret: `kubectl create -f manifests/digitalocean-secret.yaml`
2. Deploy the RBAC policies: `kubectl create -f manifests/rbac`
3. Deploy the provisioner: `kubectl create -f manifests/digitalocean-provisioner.yaml`
4. Adjust the `hostPath` in [manifests/digitalocean-flexplugin-deploy.yaml](manifests/digitalocean-flexplugin-deploy.yaml)  
   Deploy the flex plugin "installer": `kubectl create -f manifests/digitalocean-flex-plugin-deploy.yaml`
5. Modify the zone in [manifests/sc.yaml](manifests/sc.yaml)  
   Deploy the default `StorageClass`: `kubectl create -f manifests/sc.yaml`
6. (**optional**) Try it out with the example pod and PVC
   ```
   kubectl create -f examples/pvc.yaml
   kubectl create -f examples/pod-application.yaml
   ```

**TODO**
 - [ ] Support multi zones
 - [x] Rewrite flexvolume plugin in Go
 - [ ] Prevent k8s from scheduling more than 5 disks to a single droplet
 - [ ] Improve documentation
