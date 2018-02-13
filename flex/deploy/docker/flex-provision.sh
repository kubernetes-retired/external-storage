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

# ==============================================================================
# Config
# ==============================================================================
# Uncomment to disable logging to /tmp/flex-provisioner.log
#INTERNAL_DEBUG=false

# Uncomment to disable attach/detach capability
#CAPABILITY_ATTACH=false

# ==============================================================================
# Defaults and Helpers
# ==============================================================================

INTERNAL_DEBUG=${INTERNAL_DEBUG:-"true"}
CAPABILITY_ATTACH=${CAPABILITY_ATTACH:-"true"}

# Saves debug output to a log file.
debug() {
    if [ "${INTERNAL_DEBUG}" = "true" ]; then
        echo "$(date '+%Y-%m-%d %H:%M:%S') flex[$$]: $*" >> /tmp/flex-provisioner.log
    fi
}

err() {
    echo "$*" 1>&2
    debug "err() called: "$*
}

log() {
    echo "$*" >&1
    debug "log() called: "$*
}

die_notsupported() {
    log "{\"status\": \"Not supported\", \"message\": \"$*\"}"
    exit 0
}

usage() {
    err "Usage:"
    err "  $0 <action> [<params>]"
    err ""
    die_notsupported "Invalid usage of flex provisioner CLI."
}

assert_jq() {
    if ! command -v jq >/dev/null 2>&1; then
      err "{ \"status\": \"Failure\", \"message\": \"'jq' binary not found. Please install jq package before using this driver\"}"
      exit 1
    fi
}
# ==============================================================================
# Actions
# ==============================================================================
# provisions a volume
doprovision(){
    log "{\"status\": \"Success\"}"
    exit 0
}

# deletes a provisioned volume
dodelete(){
    log "{\"status\": \"Success\"}"
    exit 0
}

# Initializes the driver
doinit(){
    log "{\"status\": \"Success\", \"capabilities\": {\"attach\": ${CAPABILITY_ATTACH}}}"
    exit 0
}

# get volume's name
dogetvolumename() {
    local json_options=$1
    local node_name=$2

    log "{\"status\": \"Success\"}"
    exit 0
}

# Attach the volume specified by the given spec on the given host
doattach() {
    local json_options=$1
    local node_name=$2

    local device=''

    log "{\"status\": \"Success\", \"device\": \"$device\"}"
    exit 0
}

# Detach the volume from the Kubelet node
dodetach() {
    local mount_device=$1
    local node_name=$2

    log "{\"status\": \"Success\"}"
    exit 0
}

# Wait for the volume to be attached on the remote node
dowaitforattach() {
    local json_options=$1

    local device=''

    log "{\"status\": \"Success\", \"device\": \"$device\"}"
    exit 0
}

# Check the volume is attached on the node
doisattached() {
    local json_options=$1
    local node_name=$2

    log "{\"status\": \"Success\"}"
    exit 0
}

# Mount device mounts the device to a global path which individual pods can then bind mount.
domountdevice() {
    local mount_dir=$1
    local mount_device=$2
    local json_options=$3

    die_notsupported # = using default

    log "{\"status\": \"Success\"}"
    exit 0
}

# Mount device mounts the device to a global path which individual pods can then bind mount.
dounmountdevice() {
    local mount_device=$1

    die_notsupported # = using default
    log "{\"status\": \"Success\"}"
    exit 0
}

# Mount the volume at the mount dir
domount() {
    local mount_dir=$1
    local json_options=$2

    die_notsupported # = using default

    assert_jq
    #local fs_type="$(echo "$json_options" | jq -r '.["kubernetes.io/fsType"]')"
    local pod_name="$(echo "$json_options" | jq -r '.["kubernetes.io/pod.name"]')"
    local pod_namespace="$(echo "$json_options" | jq -r '.["kubernetes.io/pod.namespace"]')"
    #local pod_uid="$(echo "$json_options" | jq -r '.["kubernetes.io/pod.uid"]')"
    local volume_name="$(echo "$json_options" | jq -r '.["kubernetes.io/pvOrVolumeName"]')"
    local read_write="$(echo "$json_options" | jq -r '.["kubernetes.io/readwrite"]')"
    #local service_account="$(echo "$json_options" | jq -r '.["kubernetes.io/serviceAccount.name"]')"

    log "{\"status\": \"Success\"}: "$*
    exit 0
}

# unmounts the volume
dounmount() {
    local mount_dir=$1

    die_notsupported # = using default

    umount "$mount_dir"
    log "{\"status\": \"Success\"}"
    exit 0
}

# log CLI
# debug $@

op="$1"

[ -n "$op" ] || usage

shift

debug "$op() called: "$*

case "$op" in
    # Called from flex-provisioner
    provision)
        doprovision $*
        ;;
    delete)
        dodelete $*
        ;;
    # Called from kubelet/kube-controller-manager
    init)
        doinit $*
        ;;
    getvolumename)
        dogetvolumename $*
        ;;
    attach)
        doattach $*
        ;;
    detach)
        dodetach $*
        ;;
    waitforattach)
        dowaitforattach $*
        ;;
    isattached)
        doisattached $*
        ;;
    mountdevice)
        domountdevice $*
        ;;
    unmountdevice)
        dounmountdevice $*
        ;;
    mount)
        domount $*
        ;;
    unmount)
        dounmount $*
        ;;
    *)
        die_notsupported "Command $op not supported"
esac
