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
# $ shred.sh <specify number of iterations>
# If number of iterations is not specified, it defaults to 3

# Import common functions.
. $(dirname "$0")/common.sh

if [ "$1" == "-h" ]; then
  echo "Usage: $(basename $0) [ITERATIONS]"
  echo "Invokes shred on the block device specified by environment variable LOCAL_PV_BLKDEVICE"
  echo "ITERATIONS represents number of times to repeat the operation (optional). Default is 3"
  exit 0
fi

# Validate that we got a valid block device to cleanup
validateBlockDevice

iterations=3
if [ "$#" -gt 0 ]; then
    iterations=$1
fi

if ! [[ $iterations =~ ^[0-9]+$ ]]; then
    errorExit "Number of iterations is not a number $iterations"
fi

ionice -c 3 shred -vzf -n $iterations $LOCAL_PV_BLKDEVICE
