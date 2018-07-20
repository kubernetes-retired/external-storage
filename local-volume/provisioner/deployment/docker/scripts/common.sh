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

function errorExit {
    echo $@
    exit 1
}

function validateBlockDevice {
    if [ -z ${LOCAL_PV_BLKDEVICE+x} ]
    then
        errorExit "Environment variable LOCAL_PV_BLKDEVICE has not been set"
    fi

    if [ ! -b "$LOCAL_PV_BLKDEVICE" ]
    then
        errorExit "$LOCAL_PV_BLKDEVICE is not a block device."
    fi
}

function validateFilesystem {
    if [ -z ${LOCAL_PV_FILESYSTEM+x} ]
    then
        errorExit "Environment variable LOCAL_PV_FILESYSTEM has not been set"
    fi

    if [ ! -d "$LOCAL_PV_FILESYSTEM" ]
    then
        errorExit "$LOCAL_PV_FILESYSTEM is not a filesystem directory."
    fi
}

