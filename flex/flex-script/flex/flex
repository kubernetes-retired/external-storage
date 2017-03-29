#!/bin/sh

# Set this to true to log the call output to /tmp/s3fs-container
INTERNAL_DEBUG=true

INTERNAL_DEBUG=${INTERNAL_DEBUG:-"false"}

usage() {
    echo "Invalid usage of s3 provisioner CLI.. :"
    debug "Invalid usage of s3 provisioner CLI.. :"
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

