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

REGISTRY = quay.io/external_storage
IMAGE_FLEXPLUGIN = $(REGISTRY)/digitalocean-flexplugin
IMAGE_PROVISONER = $(REGISTRY)/digitalocean-provisioner

VERSION := latest

all build:
	GOOS=linux go install -v ./cmd/digitalocean-flexplugin
	CGO_ENABLED=0 GOOS=linux go build ./cmd/digitalocean-flexplugin
	GOOS=linux go install -v ./cmd/digitalocean-provisioner
	CGO_ENABLED=0 GOOS=linux go build ./cmd/digitalocean-provisioner
.PHONY: all build

container: build quick-container
.PHONY: container

quick-container:
	cp digitalocean-flexplugin deploy/docker/digitalocean-flexplugin
	docker build -t $(IMAGE_FLEXPLUGIN):$(VERSION) -f deploy/docker/Dockerfile.flexplugin deploy/docker
	cp digitalocean-provisioner deploy/docker/digitalocean-provisioner
	docker build -t $(IMAGE_PROVISONER):$(VERSION) -f deploy/docker/Dockerfile.provisioner deploy/docker
.PHONY: quick-container

push: container
	docker push $(IMAGE_FLEXPLUGIN):$(VERSION)
	docker push $(IMAGE_PROVISONER):$(VERSION)
.PHONY: push

clean:
	rm -f digitalocean-flexplugin
	rm -f deploy/docker/digitalocean-flexplugin
	rm -f digitalocean-provisioner
	rm -f deploy/docker/digitalocean-provisioner
.PHONY: clean
