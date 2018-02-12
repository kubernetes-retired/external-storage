# iSCSI-targetd provisioner 

iSCSI-targetd provisioner is an out of tree provisioner for iSCSI storage for
Kubernetes and OpenShift.  The provisioniner uses the API provided by
[targetd](https://github.com/open-iscsi/targetd) to create and export
iSCSI storage on a remote server.

## Prerequisites

iSCSI-targetd provisioner has the following prerequisistes:

1. an iSCSI server managed by `targetd`
2. all the openshift nodes correclty configured to communicate with the iSCSI server
3. sufficient disk space available as LVM2 volume group (thinly provisioned volumes are also supported and can be used to alleviate this requirement)

## How it works

When a pvc request is issued for an iscsi provisioner controlled
storage class the following happens:

1. a new volume in the configured volume group is created, the size of
the volume corresponds to the size requested in the pvc
2. the volume is exported to the first available lun and made
accessible to all the configured initiators.
3. the corresponding pv is created and bound to the pvc.


Each storage class is tied to an iSCSI target and a volume
group. Because a target can manage a maximum of 255 LUNs, each
storage class manages at most 255 pvs. iSCSI-targetd provisioner can manage
multiple storage classes.

## Installing the prerequisites

These instructions should work for RHEL/CentOS 7+ and Fedora 24+.

On Fedora 24, current updates to the SELinux policy do not work with
targetd.  There is a bug filed:
https://bugzilla.redhat.com/show_bug.cgi?id=1451139 Until this bug is
resolve, SELinux must be set to permissive mode on Fedora 25+.

For RHEL and Centos make sure you install targetd >= 0.8.6-1 as in 
previous versions there a bug that prevented exposing a volume to more 
than one initiator 

### A note about names

In various places, iSCSI Qualified Names (IQNs) need to be created.
These need to be unique.  So every target must have it's own unique
IQN, and every client (initiator) must have its own IQN.

IF NON-UNIQUE IQNs ARE USED, THEN THERE IS A POTENTIAL FOR DATA LOSS
AND BAD PERFORMANCE!

IQNs have a specific format:

iqn.YEAR-MM.com.example.blah:tag

See the [wikipedia
article](https://en.wikipedia.org/wiki/ISCSI#Addressing) for more
information.

### Configure Storage

Before configuring the iSCSI server, it needs to have storage
configured.  `targetd` uses LVM to provision storage.

If possible, it's best to have a dedicated disk or partition that can
be configured as a volume group.  However, if this is not possible, a
loopback device can be used to simulate a dedicated block device.

#### Create a Volume Group with a dedicated disk or partition

This requires an additional dedicated disk or partition to use for the
volume group.  If that's not possible, see the section on using a
loopback device.

Assuming that the dedicated block device is `/dev/vdb` and that
`targetd` is configured to use `vg-targetd`:

```
pvcreate /dev/vdb
vgcreate vg-targetd /dev/vdb
```

#### Create a Volume Group on a Loopback Device
the volume group should be called `vg-target`, this way you don' have to change any default

here is how you would do it in minishift
```
cd /var/lib/minishift
sudo dd if=/dev/zero of=disk.img bs=1G count=2
export LOOP=`sudo losetup -f`
sudo losetup $LOOP disk.img
sudo vgcreate vg-targetd $LOOP
```

#### Optional:  Enable Thin Provisioning

Logical Volumes created in a volume group are thick provisioned by
default, i.e. space is reserved at time of creation.  Optionally, a
LVM can use a thin provisioning pool to create thin provisioned volumes.  

To create a thin provisioning pool, called `pool` this example,
execute the following commands:

```
# This will create a 15GB thin pool in the vg-targetd volume group
lvcreate -L 15G --thinpool pool vg-targetd
```

When configuring `targetd`, the pool_name setting in targetd.yaml will
need to be set to <volume group name>/<thin pool name>.  In this
example, it would be `vg-targetd/pool`.

### Configure the iSCSI server

#### Install targetd and targetcli

Only `targetd` needs to be installed.  However, it's highly recommended
to also install `targetcli` as it provides a simple user interface for
looking at the state of the iSCSI system.

```
sudo yum install -y targetcli targetd
```

#### Configure target

Enable and start `target.service`.  This will ensure that iSCSI
configuration persists through reboot.

```
sudo systemctl enable target
sudo systemctl start target
```

#### Configure targetd

First, edit `/etc/target/targetd.yaml`.  A working sample
configuration is provided below:

```
password: ciao

# defaults below; uncomment and edit
# if using a thin pool, use <volume group name>/<thin pool name>
# e.g vg-targetd/pool
pool_name: vg-targetd
user: admin
ssl: false
target_name: iqn.2003-01.org.linux-iscsi.minishift:targetd
```

Next, enable and start `targetd.service`.

```
sudo systemctl enable targetd
sudo systemctl start targetd
```

#### Configure the Firewall

The default configuration requires that port 3260/tcp, 3260/udp and
18700/tcp be open on the iSCSI server.

If using `firewalld`, 

```
firewall-cmd --add-service=iscsi-target --permanent
firewall-cmd --add-port=18700/tcp --permanent 
firewall-cmd --reload
```

Otherwise, add the following iptables rules to `/etc/sysconfig/iptables`

```
TODO
```

### Configure the nodes (iscsi clients)

#### Install the iscsi-initiator-utils package

The `iscsiadm` command is required for all clients.  This is provided
by the `iscsi-initiator-utils` package and should be part of the
standard RHEL, CentOS or Fedora installation.

```
sudo yum install -y iscsi-initiator-utils
```

#### Configure the Initiator Name

Each node requires a unique initiator name.  USE OF DUPLICATE NAMES
MAY CAUSE PERFORMANCE ISSUES AND DATA LOSS.

By default, a random initiator name is generated when the
`iscsi-initiator-utils` package is installed.  This usually unique
enough, but is not guaranteed.  It's also not very descriptive.

To set a custom initiator name, edit the file `/etc/iscsi/initiatorname.iscsi`:

```
InitiatorName=iqn.2017-04.com.example:node1
```

In the above example, the initiator name is set to
`iqn.2017-04.com.example:node1`.

After changing the initiator name, restart `iscsid.service`.

```
sudo systemctl restart iscsid
```

### Install the iscsi provisioner pod in Kubernetes

Run the following commands. The secret correspond to username and password you have chosen for targetd (admin is the default for the username).
This set of command will install iSCSI-targetd provisioner in the `default` namespace.
```
export NS=default
kubectl create secret generic targetd-account --from-literal=username=admin --from-literal=password=ciao -n $NS
kubectl apply -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/kubernetes/iscsi-provisioner-d.yaml -n $NS
kubectl apply -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/kubernetes/iscsi-provisioner-pvc.yaml -n $NS
```

### Install the iscsi provisioner pod in Openshift

Run the following commands. The secret correspond to username and password you have chosen for targetd (admin is the default for the username)
```
oc new-project iscsi-provisioner
oc create sa iscsi-provisioner
oc adm policy add-cluster-role-to-user cluster-reader system:serviceaccount:iscsi-provisioner:iscsi-provisioner
# if Openshift is version < 3.6 add the iscsi-provisioner-runner role
oc create -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/openshift/iscsi-auth.yaml
# else if Openshift is version >= 3.6 add the system:persistent-volume-provisioner role
oc adm policy add-cluster-role-to-user system:persistent-volume-provisioner system:serviceaccount:iscsi-provisioner:iscsi-provisioner
#
oc secret new-basicauth targetd-account --username=admin --password=ciao
oc create -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/openshift/iscsi-provisioner-dc.yaml
```

### Start iscsi provisioner as docker container.

Alternatively, you can start a provisioner as a container locally.

```bash
docker run -ti -v /root/.kube:/kube -v /var/run/kubernetes:/var/run/kubernetes --privileged --net=host quay.io/external_storage/iscsi-controller:latest start --kubeconfig=/kube/config --master=http://127.0.0.1:8080 --log-level=debug --targetd-address=192.168.99.100 --targetd-password=ciao --targetd-username=admin
```

### Create a storage class

storage classes should look like the following
```
kind: StorageClass
apiVersion: storage.k8s.io/v1
metadata:
  name: iscsi
provisioner: iscsi
parameters:
# this id where the iscsi server is running
  targetPortal: 192.168.99.100:3260
  
# this is the iscsi server iqn  
  iqn: iqn.2003-01.org.linux-iscsi.minishift:targetd
  
# this is the iscsi interface to be used, the default is default
# iscsiInterface: default

# this must be on eof the volume groups condifgured in targed.yaml, the default is vg-targetd
# volumeGroup: vg-targetd

# this is a comma separated list of initiators that will be give access to the created volumes, they must correspond to what you have configured in your nodes.
  initiators: iqn.2017-04.com.example:node1 
  
# whether or not to use chap authentication for discovery operations  
  chapAuthDiscovery: "true"
 
# whether or not to use chap authentication for session operations  
  chapAuthSession: "true" 
  
```
you can create one with the following command in kubernetes

```
kubectl create -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/kubernetes/iscsi-provisioner-class.yaml
```
or this command in openshift
```
oc create -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/openshift/iscsi-provisioner-class.yaml
```

### Test iscsi provisioner

Create a pvc
```
oc create -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/openshift/iscsi-provisioner-pvc.yaml
```
verify that the pv has been created
```
oc get pv
```
you may also want to verify that the volume has been created in you volume group
```
targetcli ls
```
deploy a pod that uses the pvc
```
oc create -f https://raw.githubusercontent.com/kubernetes-incubator/external-storage/master/iscsi/targetd/openshift/iscsi-test-pod.yaml
```

## Installing iSCSI provisioner using ansible

If you have installed OpenShift using the ansible installer you can use a set of playbook to automate the above instructions.
You can find more documentation on these playbooks [here](./ansible/README.md)
before running the playbooks you need to annotate the inventory file with some additional variables and the nodes with the iscsi inititator name that you want to be created. Here is a summary of the variables:

| Variable Name  | Description  |
|---|---|
| targetd_lvm_volume_group |  the volume group to be created |
| targetd_lvm_physical_volumes | comma separated list of devices to add to the volume group  |
| targetd_password  | the password used to authenticate the connection to targetd, you may want to not store this on your inventory file, you can pass this as `{{ lookup('env','TARGETD_PASSWORD') }}`  |
| targetd_user |  the username used to authenticate the connection to targetd, you may want to not store this on your inventory file, you can pass this as `{{ lookup('env','TARGETD_USERNAME') }}` |
| targetd_iscsi_target | the name of the target to be created in the target server  |
| iscsi_provisioner_pullspec |  the location of the iSCSI-targetd provisioner image |
| iscsi_provisioner_default_storage_class | whether the created storage class should be the default class  |
| iscsi_provisioner_portals | optional, comma separated list of alternative IP:port where the iscsi server can be found, specifying this parameters trigger the usage of multipath |
| chap_auth_discovery | true/false  whether to use chap authentication for discovery operations |
| discovery_sendtargets_auth_username | initiator username |
| discovery_sendtargets_auth_password | initiator password, you can pass this as `{{ lookup('env','SENDTARGET_PASSWORD') }}` |
| discovery_sendtargets_auth_username_in | target username |
| discovery_sendtargets_auth_password_in | target password, you can pass this as `{{ lookup('env','SENDTARGET_PASSWORD_IN') }}` |
| chap_auth_session | true/false  whether to use chap authentication for session operations |
| session_auth_username | initiator username |
| session_auth_password | initiator password, you can pass this as `{{ lookup('env','SESSION_PASSWORD') }}` |
| session_auth_username_in | target username |
| session_auth_password_in | target password, you can pass this as `{{ lookup('env','SESSION_PASSWORD_IN') }}` |

All the nodes should have a label with their defining the initiator name for that node, here is an example:

```
ose-node1.cscc openshift_node_labels="{'region': 'primary', 'zone': 'default'}" iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-node1
ose-node2.cscc openshift_node_labels="{'region': 'primary', 'zone': 'default'}" iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-node2
```
see also the individual [roles documentation](./ansible)

To install iSCSI provisioner using ansible, run the following
```
ansible-playbook -i <your inventory file> ansible/playbook/all.yaml
```


## on iSCSI authentication

If you enable iSCSI CHAP-based authentication, the ansible installer will set the target configuration consinstently and also configure the storage class.
However at provisioning time the provisioner will not setup the chap secret. Having the permissions to setup a secret in any namespace would make the provisioner too powerful and insecure.
So, it is up to the project administrator to setup the secret.
The name of the expected secret name will be `<provisioner-name>-chap-secret` 
An example of the secret format can be found [here](./openshift/iscsi-chap-secret.yaml)

