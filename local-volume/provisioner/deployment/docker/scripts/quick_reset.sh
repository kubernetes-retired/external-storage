#!/bin/bash -e

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

# Usage:
# $ quick_reset.sh

# Import common functions.
. $(dirname "$0")/common.sh

if [ "$1" == "-h" ]; then
  echo "Usage: $(basename $0) "
  echo "Does a quick overwrite of current file system and calls wipefs to remove any signatures."
  echo "The block device must be specified by the environment variable LOCAL_PV_BLKDEVICE"
  exit 0
fi


# Validate that we got a valid block device to cleanup
validateBlockDevice

echo "Calling mkfs"
mkfs -F $LOCAL_PV_BLKDEVICE

echo "Calling wipefs"
wipefs -a $LOCAL_PV_BLKDEVICE

echo "Quick reset completed"
