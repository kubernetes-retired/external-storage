# Copyright 2018 The Kubernetes Authors.
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
IMAGE = $(REGISTRY)glusterfile-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)glusterfile-provisioner:latest

all build:
	@mkdir -p .go/src/github.com/kubernetes-incubator/external-storage/gluster/file/vendor
	@mkdir -p .go/bin
	@mkdir -p .go/stdlib
	docker run \
		--rm  \
		-e "CGO_ENABLED=0" \
		-u $$(id -u):$$(id -g) \
		-v $$(pwd)/.go:/go \
		-v $$(pwd):/go/src/github.com/kubernetes-incubator/external-storage/gluster/file \
		-v "$${PWD%/*/*}/vendor":/go/src/github.com/kubernetes-incubator/external-storage/vendor \
		-v "$${PWD%/*/*}/lib":/go/src/github.com/kubernetes-incubator/external-storage/lib \
		-v $$(pwd):/go/bin \
		-v $$(pwd)/.go/stdlib:/usr/local/go/pkg/linux_amd64_asdf \
		-w /go/src/github.com/kubernetes-incubator/external-storage/gluster/file \
		golang:1.10.3-alpine \
		go install -installsuffix "asdf" ./cmd/glusterfile-provisioner
.PHONY: all build

container: build quick-container
.PHONY: container

quick-container:
	docker build -t $(MUTABLE_IMAGE) .
	docker tag $(MUTABLE_IMAGE) $(IMAGE)
.PHONY: quick-container

push: container
	docker push $(IMAGE)
	docker push $(MUTABLE_IMAGE)
.PHONY: push

test:
	go test `go list ./... | grep -v 'vendor'`
.PHONY: test

clean:
	rm -rf .go
	rm -f glusterfile-provisioner
.PHONY: clean

