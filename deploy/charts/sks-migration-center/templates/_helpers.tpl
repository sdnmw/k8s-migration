{{- define "migration.name" -}}
sks-migration-center
{{- end -}}

{{- define "migration.labels" -}}
app.kubernetes.io/name: {{ include "migration.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}

{{- define "migration.image" -}}
{{- $repository := required "an image repository is required" .repository -}}
{{- $digest := required "a locked sha256 image digest is required" .digest -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" $digest) -}}
{{- fail "image digest must be sha256:<64 lowercase hex characters>" -}}
{{- end -}}
{{ printf "%s@%s" $repository $digest }}
{{- end -}}

