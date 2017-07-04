# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

ifeq ($(REGISTRY),)
	REGISTRY = quay.io/external_storage/
endif
ifeq ($(VERSION),)
	VERSION = latest
endif

clean: clean-aws/efs clean-ceph/cephfs clean-flex clean-gluster/block clean-local-volume/provisioner clean-nfs-client clean-nfs
.PHONY: clean

test: test-aws/efs test-local-volume/provisioner test-nfs
.PHONY: test

verify:
	repo-infra/verify/verify-go-src.sh -v
	repo-infra/verify/verify-boilerplate.sh
.PHONY: verify

aws/efs:
	cd aws/efs; \
	make container
.PHONY: aws/efs

test-aws/efs:
	cd aws/efs; \
	make test
.PHONY: test-aws/efs

clean-aws/efs:
	cd aws/efs; \
	make clean
.PHONY: clean-aws/efs

ceph/cephfs:
	cd ceph/cephfs; \
	go build cephfs-provisioner.go; \
	docker build -t $(REGISTRY)cephfs-provisioner:latest .
	docker tag $(REGISTRY)cephfs-provisioner:latest $(REGISTRY)cephfs-provisioner:$(VERSION)
.PHONY: ceph/cephfs

clean-ceph/cephfs:
	cd ceph/cephfs; \
	rm -f cephfs-provisioner
.PHONY: clean-ceph/cephfs

flex:
	cd flex; \
	make container
.PHONY: flex

clean-flex:
	cd flex; \
	make clean
.PHONY: clean-flex

gluster/block:
	cd gluster/block; \
	make container
.PHONY: gluster/block

clean-gluster/block:
	cd gluster/block; \
	make clean
.PHONY: clean-gluster/block

local-volume: local-volume/provisioner local-volume/bootstrapper
.PHONY: local-volume

test-local-volume:
	cd local-volume; \
	make test
.PHONY: test-local-volume

clean-local-volume: clean-local-volume/provisioner clean-local-volume/bootstrapper
.PHONY: clean-local-volume

local-volume/provisioner:
	cd local-volume/provisioner; \
	make container
.PHONY: local-volume/provisioner

test-local-volume/provisioner:
	cd local-volume/provisioner; \
	make test
.PHONY: test-local-volume/provisioner

clean-local-volume/provisioner:
	cd local-volume/provisioner; \
	make clean
.PHONY: clean-local-volume/provisioner

local-volume/bootstrapper:
	cd local-volume/bootstrapper; \
	make container
.PHONY: local-volume/bootstrapper

clean-local-volume/bootstrapper:
	cd local-volume/bootstrapper; \
	make clean
.PHONY: clean-local-volume/bootstrapper

nfs-client:
	cd nfs-client; \
	./build.sh; \
	docker build -t $(REGISTRY)nfs-client-provisioner:latest .
	docker tag $(REGISTRY)nfs-client-provisioner:latest $(REGISTRY)nfs-client-provisioner:$(VERSION)
.PHONY: nfs-client

clean-nfs-client:
	cd nfs-client; \
	rm -f nfs-client-provisioner
.PHONY: clean-nfs-client

nfs:
	cd nfs; \
	make container
.PHONY: nfs

test-nfs:
	cd nfs; \
	make test
.PHONY: test-nfs

clean-nfs:
	cd nfs; \
	make clean
.PHONY: clean-nfs

push-cephfs-provisioner: ceph/cephfs
	docker push $(REGISTRY)cephfs-provisioner:$(VERSION)
	docker push $(REGISTRY)cephfs-provisioner:latest
.PHONY: push-nfs-client-provisioner

push-efs-provisioner:
	cd aws/efs; \
	make push
.PHONY: push-efs-provisioner

push-glusterblock-provisioner:
	cd gluster/block; \
	make push
.PHONY: push-glusterblock-provisioner

push-local-volume-bootstrapper:
	cd local-volume/bootstrapper; \
	make push
.PHONY: push-local-volume-bootstrapper

push-local-volume-provisioner:
	cd local-volume/provisioner; \
	make push
.PHONY: push-local-volume-provisioner

push-nfs-client-provisioner: nfs-client
	docker push $(REGISTRY)nfs-client-provisioner:$(VERSION)
	docker push $(REGISTRY)nfs-client-provisioner:latest
.PHONY: push-nfs-client-provisioner

push-nfs-provisioner:
	cd nfs; \
	make push
.PHONY: push-nfs-provisioner
