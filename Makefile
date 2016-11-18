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

.PHONY: all build container quick-container push test clean

IMAGE = wongma7/nfs-provisioner

VERSION :=
TAG := $(shell git describe --abbrev=0 --tags HEAD 2>/dev/null)
COMMIT := $(shell git rev-parse HEAD)
ifeq ($(TAG),)
    VERSION := latest
else
    ifeq ($(COMMIT), $(shell git rev-list -n1 $(TAG)))
        VERSION := $(TAG)
    else
        VERSION := $(TAG)-$(COMMIT)
    endif
endif

all: build

build:
	go build

container: build
	cp nfs-provisioner deploy/docker/nfs-provisioner
	docker build -t $(IMAGE):$(VERSION) deploy/docker

quick-container:
	cp nfs-provisioner deploy/docker/nfs-provisioner
	docker build -t $(IMAGE):$(VERSION) deploy/docker

push: container
	docker push $(IMAGE):$(VERSION)

test:
	(gofmt -s -w -l `find . -type f -name "*.go" | grep -v vendor`) || exit 1
	go test `go list ./... | grep -v vendor`

clean:
	rm -f nfs-provisioner
	rm -f deploy/docker/nfs-provisioner
