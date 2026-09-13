{{- define "minio-snsd.name" -}}sks-migration-minio{{- end -}}
{{- define "minio-snsd.labels" -}}
app.kubernetes.io/name: {{ include "minio-snsd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}
{{- define "minio-snsd.image" -}}
{{- $repository := required "image.repository is required" .Values.image.repository -}}
{{- $digest := required "image.digest must be supplied by the verified offline lock" .Values.image.digest -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" $digest) -}}{{ fail "image.digest must be sha256:<64 lowercase hex characters>" }}{{- end -}}
{{ printf "%s@%s" $repository $digest }}
{{- end -}}

