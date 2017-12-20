#!/bin/sh
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

# Inspired by https://github.com/kubernetes/community/blob/3034683c5997474d9f59ef722c8ee9c1f1e58f07/contributors/design-proposals/storage/flexvolume-deployment.md#driver-deployment-script

set -o errexit
set -o pipefail

trap "exit 0" SIGTERM

VENDOR=external-storage
DRIVER=digitalocean

# Assuming the single driver file is located at /$DRIVER inside the DaemonSet image.

driver_dir=$VENDOR${VENDOR:+"~"}${DRIVER}
if [ ! -d "/flexmnt/$driver_dir" ]; then
  mkdir "/flexmnt/$driver_dir"
fi

if [ -n "$DIGITALOCEAN_ACCESS_TOKEN" ]; then
	(umask 077
	echo "$DIGITALOCEAN_ACCESS_TOKEN" > "/flexmnt/$driver_dir/do_token")
fi


cp "/$DRIVER" "/flexmnt/$driver_dir/.$DRIVER"
mv -f "/flexmnt/$driver_dir/.$DRIVER" "/flexmnt/$driver_dir/$DRIVER"

while : ; do
  sleep 3600 &
  wait
done
