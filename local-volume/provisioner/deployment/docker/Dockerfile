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

ARG GOVERSION=1.11.1
FROM golang:$GOVERSION-alpine as builder
WORKDIR /go/src/github.com/kubernetes-incubator/external-storage
ADD . .
RUN cd local-volume/provisioner; \
	CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o local-volume-provisioner ./cmd

FROM k8s.gcr.io/debian-base-amd64:0.4.0

RUN clean-install \
    util-linux \
    e2fsprogs \
    bash

COPY --from=builder /go/src/github.com/kubernetes-incubator/external-storage/local-volume/provisioner/local-volume-provisioner /local-provisioner
ADD local-volume/provisioner/deployment/docker/scripts /scripts
ENTRYPOINT ["/local-provisioner"]
