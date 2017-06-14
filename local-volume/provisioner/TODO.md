# TODO

## P0
* E2E tests (msau42)

## P1
* Can we filter the PV watch to only certain fields?
* Investigate nodename vs hostname issue (msau42)
* PV events on deletion failure
* Configmap for user parameters (ddysher)

## P2
* Partitioning, formatting, and mount extensions (needs mount propagation)
* Block device support (needs API and volume plugin changes too)
* Refactor to just use informer's cache (and need to stub out API calls for unit
  testing)
