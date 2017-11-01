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


# Usage:
#  $ dd_zero.sh  <number of iterations>
# If number of iterations is not specified, it defaults to 1

# Import common functions.
. $(dirname "$0")/common.sh

if [ "$1" == "-h" ]; then
  echo "Usage: $(basename $0) [ITERATIONS]"
  echo "Writes zeros into block device specified by environment variable LOCAL_PV_BLKDEVICE"
  echo "ITERATIONS represents number of times to repeat the operation (optional). Default is 1"
  exit 0
fi

function doZero {
  # Fill device with zeros
  cmdOut=$(dd if=/dev/zero of=$LOCAL_PV_BLKDEVICE bs=8096 2>&1 | tee /dev/stderr)
  if [[ $cmdOut !=  *"No space left on device"* ]]; then
      errorExit "Failed to find expected output from dd"
  fi
}

# Check if number of iterations have been specified.
iterations=1
if [ "$#" -gt 0 ]; then
    iterations=$1
fi

# Check if iterations are numeric
if ! [[ $iterations =~ ^[0-9]+$ ]]; then
    errorExit "Number of iterations is not a number $iterations"
fi

# Validate that we got a valid block device to cleanup
validateBlockDevice

counter=0
while [ "$counter" -lt "$iterations" ]
do
   echo "Running new iteration of dd"
   doZero
   counter=`expr $counter + 1`
done

