# TODO

## P0
* Update with Local volume API (msau42)
* Give each provisioner a unique name (msau42)
* Detect capacity of mount points
* Deploy to public image repo (msau42)
* E2E tests

## P1
* Investigate nodename vs hostname issue (msau42)
* Investigate better PV naming scheme - hashing?
* PV events on deletion failure
* Configmap for user parameters

## P2
* Partitioning, formatting, and mount extensions (needs mount propagation)
* Block device support (needs API and volume plugin changes too)
* Independent sync loops for discovery and deleter.
