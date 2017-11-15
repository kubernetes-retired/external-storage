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

set -o nounset -o errexit
exec 2>/dev/null

DIR="$(dirname "${0}")"
export PATH="${DIR}:${PATH}"
export HOME="/tmp"

if [ -z "${DIGITALOCEAN_ACCESS_TOKEN:-}" ] && [ -r "${DIR}/do_token" ]; then
	export DIGITALOCEAN_ACCESS_TOKEN="$(cat "${DIR}/do_token")"
fi

Success () {
	jq -n '{"status": "Success"}'
	exit 0
}

Failure () {
	jq -n --arg err "${1}" '{"status": "Failure", "message": $err}'
	exit 1
}

findNode () {
	local NODE="${1}"
	local err
	err="$(if [ -z "${NODE}" ]; then
		busybox wget "http://169.254.169.254/metadata/v1/id" -q -O -
	else
		# TODO: Match hostname
		doctl compute droplet list -o json | jq -r -e ".[] | select(.networks.v4[].ip_address == \"${NODE}\") | .id"
	fi)" || Failure "${err:-Node not found}"
	echo "${err}"
}

findVolumeID () {
	local err
	if ! err="$(doctl compute volume ls -o json | jq -r -e ".[] | select(.name == \"${1}\") | .id")"; then
		Failure "${err:-Volume not found}"
	fi
	echo "${err}"
}

waitForAction () {
	local ID="${1}"
	while true; do
		local status
		status="$(doctl compute action get "${ID}" -o json | jq -r .[].status)"
		if [ "${status}" = "completed" ]; then
			break
		elif [ "${status}" = "errored" ]; then
			Failure "${status}"
		fi
		sleep 2
	done
}

doAction () {
	local err
	if ! err="$(doctl compute "${@}" -o json 2>&1)"; then
		Failure "${err}"
	else
		waitForAction "$(echo "${err}" | jq -r ".[].id")"
	fi
}

attachVolume () {
	doAction volume-action attach "${1}" "${2}"
}

attach () {
	local VOLUMENAME
	VOLUMENAME="$(echo "${1}" | jq -r -e '."kubernetes.io/pvOrVolumeName"')"
	local VOLUMEID
	VOLUMEID="$(findVolumeID "${VOLUMENAME}")"

	detachVolume "${VOLUMEID}"

	local NODE
	NODE="$(findNode "${2-}")"
	if attachVolume "${VOLUMEID}" "${NODE}"; then
		local DEVICE="/dev/disk/by-id/scsi-0DO_Volume_${VOLUMENAME}"
		jq -n --arg device "${DEVICE}" '{"status": "Success", "device": $device}'
	fi
}

detachVolume () {
	local NODE
	if [ "${#}" -eq 2 ]; then
		NODE="$(findNode "${2}")"
	fi
	local ATTACHED_NODE
	ATTACHED_NODE="$(doctl compute volume ls -o json | jq -r ".[] | select(.id == \"${1}\") | .droplet_ids[]")"
	if [ -n "${ATTACHED_NODE}" ] && ([ "${#}" -ne 2 ] || [ "${NODE}" = "${ATTACHED_NODE}" ]); then
		doAction volume-action detach "${1}" "${ATTACHED_NODE}"
	fi
}

detach () {
	local VOLUMEID
	VOLUMEID="$(findVolumeID "${1}")"

	detachVolume "${VOLUMEID}" "${2}"
	Success
}

cmd="${1-}"
shift || true

case "${cmd}" in
	init)
		Success
		;;
	attach)
		attach "${@}"
		;;
	detach)
		detach "${@}"
		;;
	*)
		jq -n '{"status": "Not supported"}'
		exit 0
esac
