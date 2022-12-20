#!/bin/bash
# Replay script with headers and json to the target controller
# You can switch the targetURL to another one with the -l switch, i.e: your local debugge
# It default to http://localhost:808
# you can customze this target with the env variable GOSMEE_DEBUG_SERVICE
set -euxf
cd $(dirname $(readlink -f $0))

targetURL="{{.TargetURL}}"
[[ ${1:-""} == -l ]] && targetURL=${GOSMEE_DEBUG_SERVICE:-"http://localhost:8080"}
curl -H "Content-Type: {{ .ContentType }}" {{.Headers}} -X POST -d @./{{ .FileBase }}.json ${targetURL}
