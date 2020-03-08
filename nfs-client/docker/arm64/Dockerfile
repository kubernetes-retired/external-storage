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

FROM arm64v8/alpine:3

ADD https://github.com/multiarch/qemu-user-static/releases/download/v4.2.0-6/qemu-aarch64-static /usr/bin/qemu-aarch64-static
RUN apk update --no-cache && apk add ca-certificates && chmod +x /usr/bin/qemu-aarch64-static && ln -s /usr/bin/qemu-aarch64-static /usr/bin/qemu-arm64-static
COPY nfs-client-provisioner /nfs-client-provisioner
ENTRYPOINT ["/nfs-client-provisioner"]
