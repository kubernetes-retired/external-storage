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

SCRIPTS_DIR=${1:-/scripts}

if [ ! -d $SCRIPTS_DIR ]; then
    echo "error: please specify correct script dir"
    exit 1
fi

if ! which losetup &>/dev/null; then
    if which apt-get &>/dev/null; then
        apt-get update -y
        apt-get install -y mount
    elif which yum &>/dev/null; then
        yum install -y mount
    else
        echo "error: losetup is required, please install it"
        exit 1
    fi
fi

function block_test() {
    local script=$1
    local LOOPDEV=$(/sbin/losetup -f)
    local IMAGE=$(mktemp /tmp/localvolume.XXX)
    echo "LOOPDEV: $LOOPDEV IMAGE: $IMAGE"
    trap "losetup -d $LOOPDEV; rm $IMAGE" RETURN
    dd if=/dev/zero of=$IMAGE bs=1024 count=4096
    losetup $LOOPDEV $IMAGE
    mkfs.ext4 $LOOPDEV
    LOCAL_PV_BLKDEVICE=$LOOPDEV $SCRIPTS_DIR/$script
}

function __randome_files() {
    local dir=$1
    pushd $dir > /dev/null
    for n in $(seq 1 10); do
        mkdir $n
        pushd $n > /dev/null
        for n in $(seq 1 10); do
            touch $n
        done
        popd > /dev/null
    done
    popd > /dev/null
}

function fs_test() {
    local script=$1
    local DIR=$(mktemp -d /tmp/localvolume.XXX)
    echo "DIR: $DIR"
    trap "rm -r $DIR" RETURN
    __randome_files $DIR
    local num=$(find "$DIR" -type f | wc -l)
    echo "$num files created in $DIR"
    if [ "$num" -eq 0 ]; then
        echo "error: failed to create files in $dir"
        exit 1
    fi
    LOCAL_PV_FILESYSTEM=$DIR $SCRIPTS_DIR/$script
    if [ $? -ne 0 ]; then
        return 1
    fi
    local num=$(find "$DIR" -type f | wc -l)
    [ "$num" -eq 0 ]
}


# block
for script in blkdiscard.sh dd_zero.sh quick_reset.sh shred.sh; do
    block_test $script
    if [ $? -ne 0 ]; then
        echo "error: failed to clean block device with $script"
        exit 1
    else
        echo "Successfully clean block device with $script"
    fi
done

# fs
for script in fsclean.sh; do
    fs_test $script
    if [ $? -ne 0 ]; then
        echo "error: failed to clean filesystem directory with $script"
        exit 1
    else
        echo "Successfully clean filesystem with $script"
    fi
done
