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

set -o errexit
set -o nounset
set -o pipefail

ROOT=$(unset CDPATH && cd $(dirname "${BASH_SOURCE[0]}")/.. && pwd)
cd $ROOT

function install_helm() {
    local OS=$(uname | tr A-Z a-z)
    local VERSION=v2.7.2
    local ARCH=amd64
    local HELM_URL=http://storage.googleapis.com/kubernetes-helm/helm-${VERSION}-${OS}-${ARCH}.tar.gz
    curl -s "$HELM_URL" | sudo tar --strip-components 1 -C /usr/local/bin -zxf - ${OS}-${ARCH}/helm
}

if ! which helm &>/dev/null; then
    install_helm
fi

# lint first
ret=0
helm lint ./provisioner || ret=$?
if [ $ret -ne 0 ]; then
    echo "helm lint failed"
    exit 2
fi

# check examples
function test_values_file() {
    local input="examples/$1"
    local expected="test/expected/$1"
    local tmpfile=$(mktemp)
    trap "test -f $tmpfile && rm $tmpfile || true" EXIT
    helm template ./provisioner -f examples/$f > $tmpfile
    echo -n "Checking $input "
    local diff=$(diff -u $expected $tmpfile 2>&1) || true
    if [[ -n "${diff}" ]]; then
        echo "failed, diff: "
        echo "$diff"
        exit 1
    else
        echo "passed."
    fi
}

FILES=$(ls examples/)
for f in $FILES; do
    test_values_file $f
done
