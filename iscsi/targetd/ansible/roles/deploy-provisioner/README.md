## Here are the variables used by this role:

| variable  | optionality  | default  | description  |
|:-:|:-:|:-:|:-:|
| iscsi_provisioner_project | optional  | iscsi-provisioner  | project/namespace under which the provisioner will be deployed  |
| iscsi_provisioner_default_storage_class  |  optional | false  | whether the created storage class should be the default storage class  |
| targetd_lvm_volume_group  | optional  | vg-targetd  | the volume group managed by the created storage class  |
| chap_auth_discovery  | optional  | false  | whether chap authentication for discovery should be configured  |
| chap_auth_session  | optional  | false  | whether chap authentication for sessions should be configured  |
| iscsi_provisioner_pullspec  | optional  | quay.io/external_storage/iscsi-controller:latest  | the pullspec for the iscsi-provisioner image  |
| provisioner_name  | optional  | iscsi-targetd  | the name of the provisioner  |
| storage_class_name | optional | {{ provisioner_name }}-{{ targetd_lvm_volume_group }} | name of the created storage class |
| iscsi_initiator_name | mandatory |  | name of the iscsi initiators, this must be a host var different for each initiator | 
| targetd_iscsi_target | mandatory |  | name of the iscsi target | 
| session_auth_username | optional |  | username for session chap auth used by the initiator | 
| session_auth_password | optional |  | password for session chap auth used by the initiator |
| session_auth_username_in | optional |  | username for session chap auth used by the target |
| session_auth_password_in | optional |  | password for session chap auth used by the target |
| targetd_user  | optional  | admin  | targetd administrator username  |
| targetd_password  | optional  | admin  | targetd administrator password  |
| iscsi_provisioner_portals | optional | comma separated list of ip:port for additional portals |