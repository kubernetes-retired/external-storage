#!/bin/bash

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

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

ROOT_DIR=$(dirname "${BASH_SOURCE}")/../..
DISCOVERY_DIR="/tmp/local-disks"

# verify-prereqs verifies prereqs.
function verify-prereqs {
  which kubectl > /dev/null 2>&1 || {
    echo "error: kubectl must be installed"
    exit 1
  }
  kubectl version > /dev/null 2>&1 || {
    echo "error: a running kubernetes cluster is required"
    exit 1
  }
}

# make_containers creates containers versioned 'e2e' for e2e tests.
function make_containers {
  echo "creating e2e containers"
  pushd ${ROOT_DIR}/provisioner
  make container VERSION=e2e
  popd
  pushd ${ROOT_DIR}/bootstrapper
  make container VERSION=e2e
  popd
}

# start_bootstrapper runs bootstrapper, which launches local volume provisioner.
# e2e test uses standard bootstrapper manifest but replaces to e2e images.
function start_bootstrapper {
  echo "starting bootstrapper"
  pushd ${ROOT_DIR}
  # Create admin account for bootstrapper.
  kubectl create -f bootstrapper/deployment/kubernetes/admin-account.yaml

  # Create directory for local volume discovery.
  mkdir -p -m 777 ${DISCOVERY_DIR}
  kubectl create -f test/e2e/config_e2e.yaml

  # Create bootstrapper.
  cat bootstrapper/deployment/kubernetes/bootstrapper.yaml | \
    sed -E "s|local-volume-provisioner-bootstrap:(..*)|local-volume-provisioner-bootstrap:e2e|g" | \
    sed -E "s|local-volume-provisioner:(..*)|local-volume-provisioner:e2e|g" > \
        bootstrapper/deployment/kubernetes/bootstrapper.e2e.yaml
  kubectl create -f bootstrapper/deployment/kubernetes/bootstrapper.e2e.yaml
  rm -f bootstrapper/deployment/kubernetes/bootstrapper.e2e.yaml
  popd
}

# delete_resource deletes a resource if it exists.
# input:
#   $1 resource kind
#   $2 resource name
function delete_resource {
  kubectl get $1 -n kube-system -a | grep $2 && kubectl delete $1 -n kube-system $2
}

# cleanup deletes temporary directories on exit.
function cleanup {
  set +o errexit
  rm -rf ${DISCOVERY_DIR}
  delete_resource "pods" "local-volume-provisioner-bootstrap"
  delete_resource "ds" "local-volume-provisioner"
  delete_resource "configmap" "local-volume-config"
  delete_resource "serviceaccount" "local-storage-bootstrapper"
  delete_resource "clusterrolebinding" "local-storage:bootstrapper"
}

trap cleanup EXIT
verify-prereqs
make_containers
start_bootstrapper

go test ${ROOT_DIR}/test/e2e -v --kubeconfig=$HOME/.kube/config
