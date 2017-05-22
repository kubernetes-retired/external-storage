# localvolume controller

In 1.7 alpha release, there won't be complete scheduler code to integrate local volume with scheduler. It will still
operate on the old way where PV/PVC binding occurs at PV controller, indendent from Pod scheduling. For local volume,
this means that once the binding completes, pod requesting a PVC is essentially assigned to the node where PV is created
on. This can result in wrong or suboptimal schedule decision. In 1.7 alpha release, an external controller is needed
to work around the problem.

NOTE: controller is still in development and is not functional yet

## Quickstart

TODO

## Development

TODO

## Design

The alpha controller works by looking at all pending pods, and for each pod, check if it has local volume request. If
it does, then we put the pod into an expiration cache to check later; if there's error happens while checking volume
request, we put the pod into an inspection cache. On pod deletion event, we remove pod from corresponding cache if one
exists.

The controller has a sync loop to examine the two caches at regular interval; it unbinds pod that are pending for a
long time by recreating pv claim. Provisioner will notice pv becomes released status and recreate the pv.
