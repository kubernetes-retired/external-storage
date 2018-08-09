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
MUTABLE_IMAGE = $(REGISTRY)nfs-provisioner:latest

all build:
	GOOS=linux go build ./cmd/nfs-provisioner
.PHONY: all build

container: build quick-container
.PHONY: container

quick-container:
	cp nfs-provisioner deploy/docker/nfs-provisioner
	docker build -t $(MUTABLE_IMAGE) deploy/docker
	docker tag $(MUTABLE_IMAGE) $(IMAGE)
.PHONY: quick-container

push: container
	docker push $(IMAGE)
	docker push $(MUTABLE_IMAGE)
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
