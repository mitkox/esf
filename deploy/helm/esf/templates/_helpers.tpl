{{- define "esf.name" -}}
{{- printf "%s-esf" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "esf.image" -}}
{{- $digest := required "image.digest must be a qualified sha256 digest" .digest -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" $digest) -}}
{{- fail "image.digest must be sha256:<64 lowercase hex characters>" -}}
{{- end -}}
{{- printf "%s@%s" .repository $digest -}}
{{- end -}}
