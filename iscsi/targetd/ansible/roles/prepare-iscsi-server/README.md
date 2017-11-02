## Here are the variables used by this role:

| variable  | optionality  | default  | description  |
|:-:|:-:|:-:|:-:|
| targetd_lvm_volume_group  | optional  | vg-targetd  | the volume group managed by the created storage class  |
| chap_auth_discovery  | optional  | false  | whether chap authentication for discovery should be configured  |
| targetd_user  | optional  | admin  | targetd administrator username  |
| targetd_password  | optional  | admin  | targetd administrator password  |
| discovery_sendtargets_auth_username | mandatory |  | username for discovery chap auth used by the initiator | 
| discovery_sendtargets_auth_password | mandatory |  | password for discovery chap auth used by the initiator |
| discovery_sendtargets_auth_username_in | mandatory |  | username for discovery chap auth used by the target |
| discovery_sendtargets_auth_password_in | mandatory |  | password for discovery chap auth used by the target |
| targetd_lvm_physical_volumes | mandatory |  | comma-separated available devices |
| targetd_iscsi_target | mandatory |  | name of the iscsi target |
| chap_auth_discovery  | optional  | false  | whether chap authentication for discovery should be configured  |