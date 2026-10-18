{{- define "central.name" -}}
{{- printf "%s-central" .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
