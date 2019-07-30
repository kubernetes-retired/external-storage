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
	REGISTRY = quay.io/kubernetes_incubator/
endif
ifeq ($(VERSION),)
	VERSION = latest
endif
IMAGE = $(REGISTRY)nfs-provisioner:$(VERSION)
IMAGE_ARM = $(REGISTRY)nfs-provisioner-arm:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)nfs-provisioner:latest
MUTABLE_IMAGE_ARM = $(REGISTRY)nfs-provisioner-arm:latest


all: build quick-container build-arm quick-container-arm
.PHONY: all

build:
	GOOS=linux go build ./cmd/nfs-provisioner
.PHONY: build

build-docker:
	GOOS=linux go build -o deploy/docker/x86_64/nfs-provisioner ./cmd/nfs-provisioner
.PHONY: build-docker

build-docker-arm:
	GOOS=linux GOARCH=arm GOARM=7 go build -o deploy/docker/arm/nfs-provisioner ./cmd/nfs-provisioner
.PHONY: build-docker-arm

container: build-docker quick-container
.PHONY: container

container-arm: build-docker-arm quick-container-arm
.PHONY: container-arm

quick-container:
	docker build -t $(MUTABLE_IMAGE) deploy/docker/x86_64
	docker tag $(MUTABLE_IMAGE) $(IMAGE)
.PHONY: quick-container

quick-container-arm:
	docker run --rm --privileged multiarch/qemu-user-static --reset -p yes
	docker build -t $(MUTABLE_IMAGE_ARM) deploy/docker/arm
	docker tag $(MUTABLE_IMAGE_ARM) $(IMAGE_ARM)
.PHONY: quick-container-arm

push: container container-arm
	docker push $(IMAGE)
	docker push $(MUTABLE_IMAGE)
	docker push $(IMAGE_ARM)
	docker push $(MUTABLE_IMAGE_ARM)
.PHONY: push

push-arm: container-arm
	docker push $(IMAGE_ARM)
	docker push $(MUTABLE_IMAGE_ARM)
.PHONY: push

test-all: test test-e2e

test:
	go test `go list ./... | grep -v 'vendor\|test\|demo'`
.PHONY: test

test-e2e:
	cd ./test/e2e; ./test.sh
.PHONY: test-e2e

clean:
	rm -f nfs-provisioner
	rm -f deploy/docker/nfs-provisioner
	rm -rf test/e2e/vendor
.PHONY: clean
