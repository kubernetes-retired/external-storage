# Copyright 2016 The Kubernetes Authors.
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
IMAGE = $(REGISTRY)glusterfs-simple-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)glusterfs-simple-provisioner:latest

all build:
	GOOS=linux go install -v ./cmd/glusterfs-simple-provisioner
	GOOS=linux CGO_ENABLED=0 go build ./cmd/glusterfs-simple-provisioner
.PHONY: all build

build-mac:
	GOOS=darwin go install -v ./cmd/glusterfs-simple-provisioner
	GOOS=darwin go build ./cmd/glusterfs-simple-provisioner
.PHONY: build-mac

container: build quick-container
.PHONY: container

quick-container:
	cp glusterfs-simple-provisioner deploy/docker/glusterfs-simple-provisioner
	docker build -t $(MUTABLE_IMAGE) deploy/docker
	docker tag $(MUTABLE_IMAGE) $(IMAGE)
.PHONY: quick-container

push: container
	docker push $(IMAGE)
	docker push $(MUTABLE_IMAGE)
.PHONY: push

clean:
	rm -f glusterfs-simple-provisioner
	rm -f deploy/docker/glusterfs-simple-provisioner
.PHONY: clean
