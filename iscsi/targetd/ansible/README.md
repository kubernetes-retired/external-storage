# Ansible Playbooks for iSCSI provisioner

This folder contains simple ansible playbooks to help
configure targetd and the iSCSI provisioner.

They are intended to be used with an inventory file belonging 
to OpenShift Container Platform 3.4+ or OpenShift Origin 1.4+

## Playbooks:

* prepare-iscsi-server.yaml - Configures the targetd server, including LVM
* prepare-nodes.yaml - Configures the initiators
* deploy-provisioner.yaml - Configures OpenShift project and provisioner
* main.yaml - Executes all the above steps


## Example Host File

```
[OSEv3:children]
masters
nodes
etcd
targetd

[OSEv3:vars]
targetd_lvm_physical_volumes=/dev/vdb
targetd_iscsi_target=iqn.2003-01.org.example.mach1:1234

[targetd]
targetd.cscc

[nodes]
ose-master1.cscc openshift_node_labels="{'region': 'infra', 'zone': 'default'}" openshift_schedulable=true iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-master1
ose-master2.cscc openshift_node_labels="{'region': 'infra', 'zone': 'default'}" openshift_schedulable=true iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-master2
ose-master3.cscc openshift_node_labels="{'region': 'infra', 'zone': 'default'}" openshift_schedulable=true iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-master3
ose-node1.cscc openshift_node_labels="{'region': 'primary', 'zone': 'default'}" iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-node1
ose-node2.cscc openshift_node_labels="{'region': 'primary', 'zone': 'default'}" iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-node2
```
