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

# Vendoring the e2e framework is too hard. Download kubernetes source, patch
# our tests on top of it, build and run from there.

KUBE_VERSION=1.11.0
TEST_DIR=$GOPATH/src/github.com/kubernetes-incubator/external-storage/nfs/test/e2e

GOPATH=$TEST_DIR

# Download kubernetes source
if [ ! -e "$GOPATH/src/k8s.io/kubernetes" ]; then
  mkdir -p $GOPATH/src/k8s.io
  curl -L https://github.com/kubernetes/kubernetes/archive/v${KUBE_VERSION}.tar.gz | tar xz -C $TEST_DIR/src/k8s.io/
  rm -rf $GOPATH/src/k8s.io/kubernetes
  mv $GOPATH/src/k8s.io/kubernetes-$KUBE_VERSION $GOPATH/src/k8s.io/kubernetes
fi

cd $GOPATH/src/k8s.io/kubernetes

# Clean some unneeded sources
find ./test/e2e -maxdepth 1 -type d ! -name 'e2e' ! -name 'framework' ! -name 'manifest' ! -name 'common' ! -name 'generated' ! -name 'testing-manifests' ! -name 'perftype' -exec rm -r {} +
find ./test/e2e -maxdepth 1 -type f \( -name 'examples.go' -o -name 'gke_local_ssd.go' -o -name 'gke_node_pools.go' \) -delete
find ./test/e2e/testing-manifests -maxdepth 1 ! -name 'testing-manifests' ! -name 'BUILD' -exec rm -r {} +

# Copy our sources
mkdir ./test/e2e/storage
ln -s $TEST_DIR/nfs.go ./test/e2e/storage/
rm ./test/e2e/e2e_test.go
ln -s $TEST_DIR/e2e_test.go ./test/e2e/
cp -r $TEST_DIR/testing-manifests/* ./test/e2e/testing-manifests

# Build ginkgo and e2e.test
hack/update-bazel.sh
make ginkgo
if ! type bazel; then
  wget https://github.com/bazelbuild/bazel/releases/download/0.16.0/bazel-0.16.0-installer-linux-x86_64.sh
  chmod +x bazel-0.16.0-installer-linux-x86_64.sh
  ./bazel-0.16.0-installer-linux-x86_64.sh --user
fi
bazel build //test/e2e:gen_e2e.test
rm -f ./_output/bin/e2e.test
cp ./bazel-bin/test/e2e/e2e.test ./_output/bin

# Download kubectl to _output directory
if [ ! -e "./_output/bin/kubectl" ]; then
  curl -o ./_output/bin/kubectl -LO https://storage.googleapis.com/kubernetes-release/release/$(curl -s https://storage.googleapis.com/kubernetes-release/release/stable.txt)/bin/linux/amd64/kubectl
  chmod +x ./_output/bin/kubectl
fi

# Run tests assuming local cluster i.e. one started with hack/local-up-cluster.sh
go run hack/e2e.go -- --provider=local --check-version-skew=false --test --test_args="--ginkgo.focus=external-storage"
