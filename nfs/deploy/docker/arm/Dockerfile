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

# Modified from https://github.com/rootfs/nfs-ganesha-docker by Huamin Chen
FROM arm32v7/ubuntu:19.04

RUN apt-get update \
    && apt-get install -y nfs-ganesha nfs-ganesha-vfs dbus-x11 rpcbind hostname libnfs-utils xfsprogs libjemalloc2 libnfsidmap2 \
    && ln -sf ../proc/self/mounts /etc/mtab \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /var/run/dbus
RUN mkdir -p /export

# expose mountd 20048/tcp and nfsd 2049/tcp and rpcbind 111/tcp 111/udp
EXPOSE 2049/tcp 20048/tcp 111/tcp 111/udp

COPY nfs-provisioner /nfs-provisioner
ENTRYPOINT ["/nfs-provisioner"]
