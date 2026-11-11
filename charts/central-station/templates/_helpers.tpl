{{- define "central.name" -}}
{{- printf "%s-central" .Release.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}

{{/*
central.proxyMode renders "true" when auth.mode is proxy, and fails the render
when the mode is asked for without what makes the header a sign-in. Empty for
google mode.
*/}}
{{- define "central.proxyMode" -}}
{{- if eq .Values.auth.mode "proxy" -}}
{{- if not .Values.tailscale.enabled -}}
{{- fail "auth.mode=proxy needs tailscale.enabled=true: the identity header comes from the Tailscale operator's proxy" -}}
{{- end -}}
{{- if not .Values.networkPolicy.enabled -}}
{{- fail "auth.mode=proxy needs networkPolicy.enabled=true: without it any pod could send Tailscale-User-Login to the web or api pod" -}}
{{- end -}}
{{- if not .Values.auth.trustedProxyCIDRs -}}
{{- fail "auth.mode=proxy needs auth.trustedProxyCIDRs: the addresses the proxy's requests arrive from (the pod range minus the node's own addresses); without it the node could send the header" -}}
{{- end -}}
true
{{- else if ne .Values.auth.mode "google" -}}
{{- fail (printf "auth.mode must be google or proxy, got %q" .Values.auth.mode) -}}
{{- end -}}
{{- end -}}
