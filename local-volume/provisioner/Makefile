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

# Use:
#
# `make all` will build the provisioner
# `make container` will build a container-- you must supply a tag.
# `make push` will push the container-- you must supply a tag.

PROJECT=msau-k8s-dev
IMAGE=gcr.io/$(PROJECT)/local-volume-provisioner
GOARCH?=$(shell go env GOARCH)
GOOS?=$(shell go env GOOS)

VERSION :=
TAG := $(shell git describe --abbrev=0 --tags HEAD 2>/dev/null)
COMMIT := $(shell git rev-parse HEAD)
ifeq ($(TAG),)
    VERSION := latest
else
    ifeq ($(COMMIT), $(shell git rev-list -n1 $(TAG)))
        VERSION := $(TAG)
    else
        VERSION := latest
    endif
endif

all build:
	CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o local-volume-provisioner ./cmd

container: build
	cp local-volume-provisioner deployment/docker
	docker build --pull -t $(IMAGE):$(VERSION) deployment/docker

push: container
	gcloud docker -- push $(IMAGE):$(VERSION)

clean:
	rm -f local-volume-provisioner
	rm -f deployment/docker/local-volume-provisioner
