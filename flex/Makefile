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

IMAGE = $(REGISTRY)flex-provisioner:$(VERSION)
MUTABLE_IMAGE = $(REGISTRY)flex-provisioner:latest

all build:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o flex-provisioner ./cmd/flex-provisioner
.PHONY: all build

container: build quick-container
.PHONY: container

quick-container:
	cp flex-provisioner deploy/docker
	docker build -t $(MUTABLE_IMAGE) deploy/docker
	docker tag $(MUTABLE_IMAGE) $(IMAGE)
.PHONY: quick-container

push: container
	docker push $(IMAGE)
	docker push $(MUTABLE_IMAGE)
.PHONY: push

clean:
	rm -f flex-provisioner
	rm -f deploy/docker/flex-provisioner
.PHONY: clean
