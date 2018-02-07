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

ROOT=$(unset CDPATH && cd $(dirname "${BASH_SOURCE[0]}")/.. && pwd)
cd $ROOT

set -o errexit
set -o nounset
set -o pipefail

source "${ROOT}/hack/lib.sh"

hack::build_generators

ALL_FQ_APIS=github.com/kubernetes-incubator/external-storage/snapshot/pkg/apis/crd/v1

echo "Generating deepcopy funcs"
${OUTPUT_HOSTBIN}/deepcopy-gen --input-dirs ${ALL_FQ_APIS} -O zz_generated.deepcopy --bounding-dirs $ALL_FQ_APIS --logtostderr
