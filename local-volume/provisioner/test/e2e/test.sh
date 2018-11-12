#!/bin/bash
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

ROOT=$(unset CDPATH && cd $(dirname "${BASH_SOURCE[0]}") && pwd)
cd $ROOT

set -o errexit
set -o nounset
set -o pipefail

if [ "$(uname -s)" == "Darwin" ]; then
    echo "info: skip e2e test on osx"
    exit 0
fi

KUBECONFIG=${KUBECONFIG:-~/.kube/config}
KUBECTL=${KUBECTL:-~/kubernetes/server/bin/kubectl}

gotest_args=(
	-v
)
test_pkg=github.com/kubernetes-incubator/external-storage/local-volume/provisioner/test/e2e 
test_args=(
	-provider=local
	-kubeconfig="$KUBECONFIG"
    -kubectl-path="$KUBECTL"
	-clean-start=true
	-minStartupPods=1
)

echo go test "${gotest_args[@]}" "${test_pkg}" "${test_args[@]}"
go test -timeout 120m "${gotest_args[@]}" "${test_pkg}" "${test_args[@]}"
