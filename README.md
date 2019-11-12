Although many of these recipes still work, this repo is now deprecated, moving work to https://github.com/kubernetes-sigs/sig-storage-lib-external-provisioner, come join us there !  

# External Storage
[![Build Status](https://travis-ci.org/kubernetes-incubator/external-storage.svg?branch=master)](https://travis-ci.org/kubernetes-incubator/external-storage)
[![GoDoc](https://godoc.org/github.com/kubernetes-incubator/external-storage?status.svg)](https://godoc.org/github.com/kubernetes-incubator/external-storage)
[![Go Report Card](https://goreportcard.com/badge/github.com/kubernetes-incubator/external-storage)](https://goreportcard.com/report/github.com/kubernetes-incubator/external-storage)

## External Provisioners
This repository houses community-maintained external provisioners plus a helper library for building them. Each provisioner is contained in its own directory so for information on how to use one, enter its directory and read its documentation. The library is contained in the `lib` directory.

### What is an 'external provisioner'?
An external provisioner is a dynamic PV provisioner whose code lives out-of-tree/external to Kubernetes. Unlike [in-tree dynamic provisioners](https://kubernetes.io/docs/concepts/storage/storage-classes/#provisioner) that run as part of the Kubernetes controller manager, external ones can be deployed & updated independently.

External provisioners work just like in-tree dynamic PV provisioners. A `StorageClass` object can specify an external provisioner instance to be its `provisioner` like it can in-tree provisioners. The instance will then watch for `PersistentVolumeClaims` that ask for the `StorageClass` and automatically create `PersistentVolumes` for them. For more information on how dynamic provisioning works, see [the docs](http://kubernetes.io/docs/user-guide/persistent-volumes/) or [this blog post](https://kubernetes.io/blog/2016/10/dynamic-provisioning-and-storage-in-kubernetes/).

### How to use the library
**`lib` is deprecated. The library has moved to [kubernetes-sigs/sig-storage-lib-external-provisioner](https://github.com/kubernetes-sigs/sig-storage-lib-external-provisioner).**

## Roadmap

February
* Finalize repo structure, release process, etc.

## Community, discussion, contribution, and support

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

You can reach the maintainers of this project at:

- Slack: #sig-storage

## Kubernetes Incubator

This is a [Kubernetes Incubator project](https://github.com/kubernetes/community/blob/master/incubator.md). The project was established 2016-11-15 (as nfs-provisioner). The incubator team for the project is:

- Sponsor: Clayton (@smarterclayton)
- Champion: Jan (@jsafrane) & Brad (@childsb)
- SIG: sig-storage

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).
