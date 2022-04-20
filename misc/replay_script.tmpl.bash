#!/bin/bash
set -euxf

targetURL="{{.TargetURL}}"
## You can switch between your local debugger and your target service with -l
## default to http://localhost:8080 unless you specify a env variable GOSEC_DEBUG_URL
[[ ${1:-""} == -l ]] && targetURL=${GOSMEE_DEBUG_SERVICE:-"http://localhost:8080"}
curl -H "Content-Type: {{ .ContentType }}" {{.Headers}} -X POST -d @{{ .FilePrefix }}.json ${targetURL}