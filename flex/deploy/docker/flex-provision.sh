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

#!/bin/sh

# Set this to true to log the call output to /tmp/flex-provisioner.log
INTERNAL_DEBUG=true

INTERNAL_DEBUG=${INTERNAL_DEBUG:-"false"}

usage() {
    echo "Invalid usage of flex provisioner CLI.. :"
    debug "Invalid usage of flex provisioner CLI.. :"
    exit 1
}

err() {
	echo -ne $* 1>&2
}

log() {
  	echo -n $* >&1
  	debug "log() called:"$*

}

# Saves debug output to a log file.
debug() {
    if [ "${INTERNAL_DEBUG}" == "true" ]; then
        echo $* >> /tmp/flex-provisioner.log
    fi
}

# checks if the resource is mounted
ismounted() {
    debug "ismounted() called"

    echo 0
}

# deletes a provisioned volume
delete(){
    debug "delete() called"
    log "{\"status\": \"Success\"}"
    exit 0
}

# provisions a volume
provision(){
    debug "provision() called"
    log "{\"status\": \"Success\"}"
    exit 0

}

# attaches a volume to host
attach() {
    debug "attach() called"
    log "{\"status\": \"Success\"}"
	exit 0
}

# detaches a volume from host
detach() {
    debug "detach() called"
	log "{\"status\": \"Success\"}"
	exit 0
}

# mounts the volume
domount() {
    debug "domount() called"
    log "{\"status\": \"Success\"}"
	exit 0
}

# unmounts the volume
unmount() {
    debug "unmount() called"

	log "{\"status\": \"Success\"}"
	exit 0
}

# log CLI
# debug $@

log "hello"

op=$1

if [ "$op" = "init" ]; then
	log "{\"status\": \"Success\"}"
	exit 0
fi

shift
case "$op" in
	attach)
		attach $*
		;;
	detach)
		detach $*
		;;
	provision)
		provision $*
		;;
	delete)
        delete $*
        ;;
	mount)
		domount $*
		;;
	unmount)
		unmount $*
		;;
	*)
		usage
esac

exit 1

