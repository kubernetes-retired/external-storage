# Kubernetes external FLEX provisioner

This is an example external provisioner for kubernetes meant for use with FLEX based volume plugins.  The provisioner runs in a pod, so the shell script which provisions/deletes the volumes must also be included in the PODs container (and not on the host).

**First Steps**

Before building and packaging this, you need to include the shell script which flex will use for provisioning.  The shell script path must mach whats in the provisioning container.

The current example is in flex/flex/flex  and is specified in examples/pod.yaml here:
*-execCommand=/opt/go/src/github.com/childsb/flex-provisioner/flex/flex/flex* 

If you copy in a new file or change the path, update the flag in the POD.yaml.

**To Build**

```bash
make
```

**To Deploy**

You can use the example provisioner pod to deploy ```kubectl create -f examples/pod-provisioner.yaml```

