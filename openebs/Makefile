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

.PHONY: image clean bootstrap deps build image
.DEFAULT_GOAL := build



build: $(shell find . -name "*.go")
	CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o openebs-provisioner .

push: 
	@cp openebs-provisioner buildscripts/docker/
	@cd buildscripts/docker && sudo docker build -t openebs/openebs-provisioner:ci .

deploy:
	@cp openebs-provisioner buildscripts/docker/
	@cd buildscripts/docker && sudo docker build -t ${DIMAGE}:latest .
	@sh buildscripts/push

clean:
	rm -rf vendor
	rm -f openebs-provisioner
	rm -f buildscripts/docker/openebs-provisioner
