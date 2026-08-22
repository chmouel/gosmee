{{/*
Expand the name of the chart.
*/}}
{{- define "gosmee.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "gosmee.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "gosmee.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gosmee.labels" -}}
helm.sh/chart: {{ include "gosmee.chart" . }}
{{ include "gosmee.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "gosmee.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gosmee.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gosmee.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gosmee.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container image, digest wins over tag when both are set.
*/}}
{{- define "gosmee.image" -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}
{{- end }}

{{/*
Public URL advertised to clients, derived from the ingress or the HTTPRoute when
not set explicitly.
*/}}
{{- define "gosmee.publicURL" -}}
{{- if .Values.server.publicUrl -}}
{{- .Values.server.publicUrl | trimSuffix "/" -}}
{{- else if and .Values.ingress.enabled .Values.ingress.hosts -}}
{{- $host := (first .Values.ingress.hosts).host -}}
{{- if $host -}}
{{- $scheme := ternary "https" "http" (gt (len .Values.ingress.tls) 0) -}}
{{- printf "%s://%s" $scheme $host -}}
{{- end -}}
{{- else if and .Values.httpRoute.enabled .Values.httpRoute.hostnames -}}
{{- printf "https://%s" (first .Values.httpRoute.hostnames) -}}
{{- end -}}
{{- end }}

{{/*
Secret holding the webhook signatures, replay token and Redis URL.
*/}}
{{- define "gosmee.secretName" -}}
{{- if .Values.server.secret.existingSecret -}}
{{- .Values.server.secret.existingSecret -}}
{{- else -}}
{{- include "gosmee.fullname" . -}}
{{- end -}}
{{- end }}

{{- define "gosmee.createSecret" -}}
{{- if .Values.server.secret.existingSecret -}}
{{- else if or .Values.server.secret.webhookSignatures .Values.server.secret.replayToken .Values.server.redis.url -}}
true
{{- end -}}
{{- end }}

{{- define "gosmee.useSecret" -}}
{{- if or .Values.server.secret.existingSecret (include "gosmee.createSecret" .) -}}
true
{{- end -}}
{{- end }}

{{/*
Secret holding the encrypted channels JSON document.
*/}}
{{- define "gosmee.encryptedChannelsSecretName" -}}
{{- if .Values.server.encryptedChannels.existingSecret -}}
{{- .Values.server.encryptedChannels.existingSecret -}}
{{- else -}}
{{- printf "%s-encrypted-channels" (include "gosmee.fullname" .) -}}
{{- end -}}
{{- end }}

{{- define "gosmee.encryptedChannelsMountPath" -}}
{{- printf "/etc/gosmee/encrypted-channels/%s" .Values.server.encryptedChannels.key -}}
{{- end }}

{{/*
Validate the values that cannot produce a working server.
*/}}
{{- define "gosmee.validateValues" -}}
{{- if and (gt (int .Values.replicaCount) 1) (not .Values.server.redis.url) (not .Values.server.secret.existingSecret) -}}
{{- fail "replicaCount > 1 needs server.redis.url (or a GOSMEE_REDIS_URL key in server.secret.existingSecret), otherwise a webhook only reaches the clients connected to the replica that received it" -}}
{{- end -}}
{{- if .Values.server.encryptedChannels.enabled -}}
{{- if and (not .Values.server.encryptedChannels.channels) (not .Values.server.encryptedChannels.existingSecret) -}}
{{- fail "server.encryptedChannels.enabled needs either server.encryptedChannels.channels or server.encryptedChannels.existingSecret" -}}
{{- end -}}
{{- end -}}
{{- if and .Values.httpRoute.enabled (not .Values.httpRoute.parentRefs) -}}
{{- fail "httpRoute.enabled needs at least one entry in httpRoute.parentRefs" -}}
{{- end -}}
{{- end }}
