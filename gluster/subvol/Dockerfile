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

FROM centos:7

LABEL maintainer="Niels de Vos <ndevos@redhat,com>"

# 1. install glusterfs-fuse to make mounting possible
# 2. get all available updates
# 3. remove all repository cache
RUN true \
    && yum -y install centos-release-gluster && yum -y install glusterfs-fuse \
    && yum -y update \
    && yum --enablerepo=* clean all \
    && true

# copy the executable from the build container
COPY gluster-subvol-provisioner /usr/local/sbin/gluster-subvol-provisioner

# mounting FUSE filesystems may require loading the fuse.ko kernel module
VOLUME ["/dev/fuse"]
VOLUME ["/usr/lib/modules"]

ENTRYPOINT ["/usr/local/sbin/gluster-subvol-provisioner"]
