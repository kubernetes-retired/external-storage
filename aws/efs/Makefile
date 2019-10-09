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
IMAGE = $(REGISTRY)efs-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)efs-provisioner:latest

all build:
	go build ./cmd/efs-provisioner
.PHONY: all build

container: quick-container
.PHONY: container

quick-container:
	docker build -t $(MUTABLE_IMAGE) -f Dockerfile ../..
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
	rm -f efs-provisioner
.PHONY: clean
