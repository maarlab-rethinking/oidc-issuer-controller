{{- define "oidc-issuer-controller.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "oidc-issuer-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "oidc-issuer-controller.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "oidc-issuer-controller.selectorLabels" -}}
app.kubernetes.io/name: oidc-issuer-controller
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "oidc-issuer-controller.labels" -}}
{{ include "oidc-issuer-controller.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}
