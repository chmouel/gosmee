#!/usr/bin/env bash
# Copyright 2023 Chmouel Boudjnah <chmouel@chmouel.com>
# Replay script with headers and JSON payload to the target controller.
#
# You can switch the targetURL with the first command line argument and you can
# the -l switch, which defaults to http://localhost:8080.
# Same goes for the variable GOSMEE_DEBUG_SERVICE.
#
set -euxfo pipefail
cd $(dirname $(readlink -f $0))

targetURL="{{ .TargetURL }}"
if [[ ${1:-""} == -l ]]; then
	targetURL="http://localhost:8080"
elif [[ -n ${1:-""} ]]; then
	targetURL=${1}
elif [[ -n ${GOSMEE_DEBUG_SERVICE:-""} ]]; then
	targetURL=${GOSMEE_DEBUG_SERVICE}
fi

curl -sSi -H "Content-Type: {{ .ContentType }}" {{ .Headers }} -X POST -d @./{{ .FileBase }}.json ${targetURL}
