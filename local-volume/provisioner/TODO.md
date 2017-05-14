# TODO

## P0
* Update with Local volume API (msau42)
* Give each provisioner a unique name
* Detect capacity of mount points
* Deploy to public image repo
* E2E tests

## P1
* Investigate nodename vs hostname issue
* Investigate better PV naming scheme - hashing?
* PV events on deletion failure
* Configmap for user parameters
* verification of underlying volume - make sure it's file-based and not block

## P2
* Partitioning, formatting, and mount extensions (needs mount propagation)
* Block device support (needs API and volume plugin changes too)
* Independent sync loops for discovery and deleter.
