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
        REGISTRY = docker.io/ibmcom/
endif
ifeq ($(VERSION),)
        VERSION = latest
endif
IMAGE_PPC64LE = $(REGISTRY)nfs-client-provisioner-ppc64le:$(VERSION) 
MUTABLE_IMAGE_PPC64LE = $(REGISTRY)nfs-client-provisioner-ppc64le:latest

all: build_ppc64le image_ppc64le

container: build_ppc64le image_ppc64le

ppc64le: build_ppc64le image_ppc64le

build_ppc64le:
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-extldflags "-static"' -o docker/ppc64le/nfs-client-provisioner ./cmd/nfs-client-provisioner

image_ppc64le:
	docker build -t $(MUTABLE_IMAGE_PPC64LE) docker/ppc64le
	docker tag $(MUTABLE_IMAGE_PPC64LE) $(IMAGE_PPC64LE)

push:
	docker push $(IMAGE_PPC64LE)
	docker push $(MUTABLE_IMAGE_PPC64LE)
