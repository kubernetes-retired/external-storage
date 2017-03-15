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

IMAGE = quay.io/external_storage/efs-provisioner
# TODO
VERSION = latest

all build:
	@mkdir -p .go/src/github.com/kubernetes-incubator/external-storage/aws/efs/vendor
	@mkdir -p .go/bin
	@mkdir -p .go/stdlib
	docker run \
		--rm  \
		-e "CGO_ENABLED=0" \
		-u $$(id -u):$$(id -g) \
		-v $$(pwd)/.go:/go \
		-v $$(pwd):/go/src/github.com/kubernetes-incubator/external-storage/aws/efs \
		-v "$${PWD%/*/*}/vendor":/go/src/github.com/kubernetes-incubator/external-storage/vendor \
		-v "$${PWD%/*/*}/lib":/go/src/github.com/kubernetes-incubator/external-storage/lib \
		-v $$(pwd):/go/bin \
		-v $$(pwd)/.go/stdlib:/usr/local/go/pkg/linux_amd64_asdf \
		-w /go/src/github.com/kubernetes-incubator/external-storage/aws/efs \
		golang:1.7.4-alpine \
		go install -installsuffix "asdf" ./cmd/efs-provisioner
.PHONY: all build

container: build quick-container
.PHONY: container

quick-container:
	docker build -t $(IMAGE):$(VERSION) .
.PHONY: quick-container

push: container
	docker push $(IMAGE):$(VERSION)
.PHONY: push

test: verify
	go test `go list ./... | grep -v 'vendor'`
.PHONY: test

verify:
	@tput bold; echo Running gofmt:; tput sgr0
	(gofmt -s -w -l `find . -type f -name "*.go" | grep -v vendor`) || exit 1
	@tput bold; echo Running golint and go vet:; tput sgr0
	for i in $$(find . -type f -name "*.go" | grep -v 'vendor\|minmax'); do \
		golint --set_exit_status $$i || exit 1; \
		go vet $$i; \
	done
	@tput bold; echo Running verify-boilerplate; tput sgr0
	../../repo-infra/verify/verify-boilerplate.sh
.PHONY: verify

clean:
	rm -rf .go
	rm -f efs-provisioner
.PHONY: clean
