#!/bin/bash
#
# Replay script with headers and JSON payload to the target controller.
#
# You can switch the targetURL to another one with the -l switch, which defaults
# to http://localhost:8080.
#
# You can customze this target with the env variable: GOSMEE_DEBUG_SERVICE
set -euxf
cd $(dirname $(readlink -f $0))

targetURL="{{ .TargetURL }}"
[[ ${1:-""} == -l ]] && targetURL=${GOSMEE_DEBUG_SERVICE:-"http://localhost:8080"}
curl -sSi -H "Content-Type: {{ .ContentType }}" {{ .Headers }} -X POST -d @./{{ .FileBase }}.json ${targetURL}
