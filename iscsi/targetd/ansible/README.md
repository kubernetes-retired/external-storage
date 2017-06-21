# Ansible Playbooks for iSCSI provisioner

This folder contains simple ansible playbooks to help
configure targetd and the iSCSI provisioner.

They are intended to be used with an inventory file belonging 
to OpenShift Container Platform 3.4+ or OpenShift Origin 1.4+

## Playbooks:

* targetd-playbook.yaml - Configures the targetd server, including LVM
* initiator-playbook.yaml - Configures the initiators
* provisioner-playbook.yaml - Configures OpenShift project and provisioner

These should be run in order above.

## Bugs

Currently, it appears that the targetd server needs to be rebooted if 
firewalld is not currently installed.

## Example Host File

```
[OSEv3:children]
masters
nodes
etcd
targetd

[OSEv3:vars]
targetd_lvm_volume_group=vg-targetd
targetd_lvm_physical_volume=/dev/vdb
targetd_password=ciao
targetd_user=admin
targetd_iscsi_target=iqn.2003-01.org.example.mach1:1234
iscsi_provisioner_pullspec=raffaelespazzoli/iscsi-controller:0.0.1
iscsi_provisioner_default_storage_class=true

[targetd]
targetd.cscc

[nodes]
ose-master1.cscc openshift_node_labels="{'region': 'infra', 'zone': 'default'}" openshift_schedulable=true iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-master1
ose-master2.cscc openshift_node_labels="{'region': 'infra', 'zone': 'default'}" openshift_schedulable=true iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-master2
ose-master3.cscc openshift_node_labels="{'region': 'infra', 'zone': 'default'}" openshift_schedulable=true iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-master3
ose-node1.cscc openshift_node_labels="{'region': 'primary', 'zone': 'default'}" iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-node1
ose-node2.cscc openshift_node_labels="{'region': 'primary', 'zone': 'default'}" iscsi_initiator_name=iqn.2003-03.net.deadvax:ose-node2
```
