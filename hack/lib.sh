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

OS=$(go env GOOS)
ARCH=$(go env GOARCH)
PLATFORM=$(uname -s | tr A-Z a-z)
ROOT=$(unset CDPATH && cd $(dirname "${BASH_SOURCE[0]}")/.. && pwd)
OUTPUT_HOSTBIN=${GOPATH}/bin

function hack::build_generators() {
    # NOTE: Add target if needed, available targets:
    #   conversion-gen
    #   client-gen
    #   lister-gen
    #   informer-gen
    #   go-to-protobuf
    #   go-to-protobuf/protoc-gen-gogo
    local targets=(
        deepcopy-gen
    )
    local need_regenerated=true
    # TODO find a way to avoid regenerating if possible
    if ! $need_regenerated; then
        return
    fi
    local VERSION=1.9.2
    local TARURL=https://github.com/kubernetes/code-generator/archive/kubernetes-${VERSION}.tar.gz
    echo "Building ${targets[*]} from k8s.io/code-generator..."
    local TMPDIR=$(mktemp -d -t goXXXX)
    trap "rm -r $TMPDIR && echo $TMPDIR removed" EXIT
    echo "Go build directory: $TMPDIR"
    mkdir ${TMPDIR}/{bin,pkg,src}
    mkdir -p ${TMPDIR}/src/k8s.io/code-generator
    wget -q $TARURL -O - | tar -zxf - --strip-components 1 -C $TMPDIR/src/k8s.io/code-generator
    for target in ${targets[*]}; do
        GOPATH=$TMPDIR go build -o ${OUTPUT_HOSTBIN}/$(basename ${target}) k8s.io/code-generator/cmd/${target}
    done
}
