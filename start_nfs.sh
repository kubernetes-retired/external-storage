#!/bin/bash

# Copyright 2015 The Kubernetes Authors.
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

# start rpcbind if it is not started yet
/usr/sbin/rpcinfo 127.0.0.1 > /dev/null; s=$?
if [ $s -ne 0 ]; then
    echo "Starting rpcbind"
    /usr/sbin/rpcbind -w
fi

mount -t nfsd nfds /proc/fs/nfsd

# -N 4.x: disable NFSv4
# -V 3: enable NFSv3
/usr/sbin/rpc.mountd -N 2 -V 3 -N 4 -N 4.1

/usr/sbin/exportfs -r
# -G 10 to reduce grace time to 10 seconds (the lowest allowed)
/usr/sbin/rpc.nfsd -G 10 -N 2 -V 3 -N 4 -N 4.1 2
/usr/sbin/rpc.statd --no-notify
echo "NFS started"
