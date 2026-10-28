{{/*
Names, labels and the shared pod and container settings of the Birge chart.
*/}}

{{- define "birge.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "birge.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Common labels. Call with (dict "root" $ "component" "api"). */}}
{{- define "birge.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
app.kubernetes.io/part-of: birge
{{ include "birge.selectorLabels" . }}
{{- end -}}

{{/* Selector labels: stable across upgrades, used by Services and NetworkPolicies. */}}
{{- define "birge.selectorLabels" -}}
app.kubernetes.io/name: {{ include "birge.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* Image reference; a digest wins over the tag. Call with (dict "root" $ "image" .Values.image). */}}
{{- define "birge.image" -}}
{{- if .image.digest -}}
{{- printf "%s@%s" .image.repository .image.digest -}}
{{- else -}}
{{- printf "%s:%s" .image.repository (default .root.Chart.AppVersion .image.tag) -}}
{{- end -}}
{{- end -}}

{{/* Pod-level security: non-root, seccomp RuntimeDefault, no API token. */}}
{{- define "birge.podSecurity" -}}
automountServiceAccountToken: false
securityContext:
  runAsNonRoot: true
  runAsUser: {{ .Values.securityContext.runAsUser }}
  runAsGroup: {{ .Values.securityContext.runAsGroup }}
  fsGroup: {{ .Values.securityContext.fsGroup }}
  seccompProfile:
    type: RuntimeDefault
{{- with .Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}

{{/* Container-level security: read-only root, no privilege escalation, no capabilities. */}}
{{- define "birge.containerSecurity" -}}
securityContext:
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  capabilities:
    drop: ["ALL"]
  seccompProfile:
    type: RuntimeDefault
{{- end -}}

{{/* Fixed container ports. The internal and metrics ports are never routed by the ingress. */}}
{{- define "birge.httpPort" -}}8080{{- end -}}
{{- define "birge.internalPort" -}}8090{{- end -}}
{{- define "birge.metricsPort" -}}9090{{- end -}}

{{/* True when a comma list (string) contains a value. Call with (list "fcm,apns" "apns"). */}}
{{- define "birge.listHas" -}}
{{- $items := splitList "," (index . 0 | toString) -}}
{{- $found := false -}}
{{- range $items -}}{{- if eq (trim .) (index $ 1) -}}{{- $found = true -}}{{- end -}}{{- end -}}
{{- if $found -}}true{{- end -}}
{{- end -}}

{{/*
Secret files of a component as a YAML list of {env, name, key, file}: each
is projected to /secrets/<file> and, when env is set, the variable points
there. Only references: the chart never sees a secret value (non-negotiable
4). Call with (dict "root" $ "component" "api").
*/}}
{{- define "birge.secretFiles" -}}
{{- $v := .root.Values -}}
{{- $s := $v.secrets -}}
{{- $c := .component -}}
{{- $files := list -}}
{{- $files = append $files (dict "env" "BIRGE_KEK_KEYSET_FILE" "ref" $s.kek "file" "kek.json") -}}
{{- $files = append $files (dict "env" "BIRGE_HASH_KEY_FILE" "ref" $s.hash "file" "hash.key") -}}
{{- $files = append $files (dict "env" "BIRGE_PLAN_LINK_KEY_FILE" "ref" $s.planLink "file" "plan-link.key") -}}
{{- if $s.blindIndex.name -}}
{{- $files = append $files (dict "env" "BIRGE_BLIND_INDEX_KEY_FILE" "ref" $s.blindIndex "file" "blind-index.key") -}}
{{- end -}}
{{- range $s.previousKeks -}}
{{- $files = append $files (dict "env" "" "ref" . "file" (printf "kek-%s.json" .id)) -}}
{{- end -}}
{{- if $v.config.s3.enabled -}}
{{- $files = append $files (dict "env" "BIRGE_S3_ACCESS_KEY_FILE" "ref" $s.s3AccessKey "file" "s3-access-key") -}}
{{- $files = append $files (dict "env" "BIRGE_S3_SECRET_KEY_FILE" "ref" $s.s3SecretKey "file" "s3-secret-key") -}}
{{- end -}}
{{- if eq $c "api" -}}
{{- $files = append $files (dict "env" "BIRGE_POW_KEY_FILE" "ref" $s.pow "file" "pow.key") -}}
{{- if $v.staff.enabled -}}
{{- $files = append $files (dict "env" "BIRGE_STAFF_CLIENT_SECRET_FILE" "ref" $s.staffClientSecret "file" "staff-client-secret") -}}
{{- end -}}
{{- if $s.llmApiKey.name -}}
{{- $files = append $files (dict "env" "BIRGE_LLM_API_KEY_FILE" "ref" $s.llmApiKey "file" "llm-api-key") -}}
{{- end -}}
{{- end -}}
{{- if eq $c "worker" -}}
{{- $files = append $files (dict "env" "BIRGE_ANALYTICS_KEY_FILE" "ref" $s.analytics "file" "analytics.key") -}}
{{- if $v.courierHook.enabled -}}
{{- $files = append $files (dict "env" "BIRGE_COURIER_SECRET_FILE" "ref" $s.courierHook "file" "courier-hook.secret") -}}
{{- end -}}
{{- if include "birge.listHas" (list $v.delivery.pushProvider "fcm") -}}
{{- $files = append $files (dict "env" "BIRGE_FCM_SERVICE_ACCOUNT_FILE" "ref" $s.fcmServiceAccount "file" "fcm-service-account.json") -}}
{{- end -}}
{{- if include "birge.listHas" (list $v.delivery.pushProvider "apns") -}}
{{- $files = append $files (dict "env" "BIRGE_APNS_KEY_FILE" "ref" $s.apnsKey "file" "apns-key.p8") -}}
{{- end -}}
{{- if include "birge.listHas" (list $v.delivery.smsProvider "smsc") -}}
{{- $files = append $files (dict "env" "BIRGE_SMSC_LOGIN_FILE" "ref" $s.smscLogin "file" "smsc-login") -}}
{{- $files = append $files (dict "env" "BIRGE_SMSC_PASSWORD_FILE" "ref" $s.smscPassword "file" "smsc-password") -}}
{{- end -}}
{{- end -}}
{{- toYaml $files -}}
{{- end -}}

{{/* Projected /secrets volume of a component. Call with (dict "root" $ "component" "api"). */}}
{{- define "birge.secretsVolume" -}}
- name: secrets
  projected:
    # Readable by the pod group (fsGroup) only; birge reads them at start.
    defaultMode: 0440
    sources:
{{- range (include "birge.secretFiles" . | fromYamlArray) }}
      - secret:
          name: {{ required (printf "a Secret name for /secrets/%s" .file) .ref.name }}
          items:
            - key: {{ required (printf "a Secret key for /secrets/%s" .file) .ref.key }}
              path: {{ .file }}
{{- end }}
{{- end -}}

{{/* Variable map (name -> value) rendered as env entries, sorted for stable diffs. */}}
{{- define "birge.envMap" -}}
{{- range $k := keys . | sortAlpha }}
- name: {{ $k }}
  value: {{ index $ $k | toString | quote }}
{{- end }}
{{- end -}}

{{/* Environment of the API and the worker. Call with (dict "root" $ "component" "api"). */}}
{{- define "birge.env" -}}
{{- $v := .root.Values -}}
{{- $c := .component -}}
{{- $env := dict -}}
{{- $_ := set $env "BIRGE_HTTP_ADDR" (printf ":%s" (include "birge.httpPort" .)) -}}
{{- if $v.metrics.enabled -}}
{{- $_ := set $env "BIRGE_METRICS_ADDR" (printf ":%s" (include "birge.metricsPort" .)) -}}
{{- end -}}
{{- $_ := set $env "NATS_URL" $v.config.natsUrl -}}
{{- $_ := set $env "KRATOS_PUBLIC_URL" $v.config.kratosPublicUrl -}}
{{- $_ := set $env "KRATOS_ADMIN_URL" $v.config.kratosAdminUrl -}}
{{- $_ := set $env "BIRGE_KEK_ID" (required "config.kekId" $v.config.kekId) -}}
{{- $_ := set $env "BIRGE_PUBLIC_URL" $v.config.publicUrl -}}
{{- $_ := set $env "BIRGE_SMS_ALLOWED_PREFIXES" $v.config.smsAllowedPrefixes -}}
{{- $_ := set $env "BIRGE_TIME_ZONE" $v.config.timeZone -}}
{{- $_ := set $env "BIRGE_PRACTICES_BASE_URL" $v.config.practicesBaseUrl -}}
{{- $_ := set $env "BIRGE_RATE_LIMIT_STORE" $v.config.rateLimitStore -}}
{{- if $v.secrets.previousKeks -}}
{{- $prev := list -}}
{{- range $v.secrets.previousKeks -}}
{{- $prev = append $prev (printf "%s=/secrets/kek-%s.json" (required "secrets.previousKeks[].id" .id) .id) -}}
{{- end -}}
{{- $_ := set $env "BIRGE_KEK_PREVIOUS_KEYSET_FILES" (join "," $prev) -}}
{{- end -}}
{{- if $v.config.otlpEndpoint -}}
{{- $_ := set $env "OTEL_EXPORTER_OTLP_ENDPOINT" $v.config.otlpEndpoint -}}
{{- end -}}
{{- with $v.config.retrieval -}}
{{- if .teiUrl }}{{ $_ := set $env "BIRGE_TEI_URL" .teiUrl }}{{ end -}}
{{- if .qdrantUrl }}{{ $_ := set $env "BIRGE_QDRANT_URL" .qdrantUrl }}{{ end -}}
{{- if .embeddingModelId }}{{ $_ := set $env "BIRGE_EMBEDDING_MODEL_ID" .embeddingModelId }}{{ end -}}
{{- end -}}
{{- if $v.config.s3.enabled -}}
{{- $_ := set $env "BIRGE_S3_ENDPOINT" (required "config.s3.endpoint" $v.config.s3.endpoint) -}}
{{- $_ := set $env "BIRGE_S3_BUCKET" (required "config.s3.bucket" $v.config.s3.bucket) -}}
{{- $_ := set $env "BIRGE_S3_REGION" $v.config.s3.region -}}
{{- $_ := set $env "BIRGE_S3_PUBLIC_BASE_URL" (required "config.s3.publicBaseUrl" $v.config.s3.publicBaseUrl) -}}
{{- end -}}
{{- if eq $c "api" -}}
{{- if $v.config.trustedProxyHeader -}}
{{- $_ := set $env "BIRGE_TRUSTED_PROXY_HEADER" $v.config.trustedProxyHeader -}}
{{- end -}}
{{- $origins := $v.config.chatWsOrigins -}}
{{- if and (not $origins) $v.ingress.enabled -}}{{- $origins = $v.ingress.member.host -}}{{- end -}}
{{- if $origins -}}
{{- $_ := set $env "BIRGE_CHAT_WS_ORIGINS" $origins -}}
{{- end -}}
{{- if $v.staff.enabled -}}
{{- with $v.staff -}}
{{- $_ := set $env "BIRGE_STAFF_INTROSPECTION_URL" (required "staff.introspectionUrl" .introspectionUrl) -}}
{{- $_ := set $env "BIRGE_STAFF_CLIENT_ID" (required "staff.clientId" .clientId) -}}
{{- $_ := set $env "BIRGE_STAFF_ISSUER" (required "staff.issuer" .issuer) -}}
{{- $_ := set $env "BIRGE_STAFF_AUDIENCE" (required "staff.audience" .audience) -}}
{{- if .rolesClaim }}{{ $_ := set $env "BIRGE_STAFF_ROLES_CLAIM" .rolesClaim }}{{ end -}}
{{- if .roleMap }}{{ $_ := set $env "BIRGE_STAFF_ROLE_MAP" .roleMap }}{{ end -}}
{{- if .mfaAcr }}{{ $_ := set $env "BIRGE_STAFF_MFA_ACR" .mfaAcr }}{{ end -}}
{{- if .mfaAmr }}{{ $_ := set $env "BIRGE_STAFF_MFA_AMR" .mfaAmr }}{{ end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if eq $c "worker" -}}
{{- with $v.delivery -}}
{{- $_ := set $env "BIRGE_DELIVERY_MODE" .mode -}}
{{- $_ := set $env "BIRGE_PUSH_PROVIDER" .pushProvider -}}
{{- $_ := set $env "BIRGE_SMS_PROVIDER" .smsProvider -}}
{{- if .smscSender }}{{ $_ := set $env "BIRGE_SMSC_SENDER" .smscSender }}{{ end -}}
{{- if include "birge.listHas" (list .pushProvider "apns") -}}
{{- $_ := set $env "BIRGE_APNS_KEY_ID" (required "delivery.apns.keyId" .apns.keyId) -}}
{{- $_ := set $env "BIRGE_APNS_TEAM_ID" (required "delivery.apns.teamId" .apns.teamId) -}}
{{- $_ := set $env "BIRGE_APNS_TOPIC" (required "delivery.apns.topic" .apns.topic) -}}
{{- $_ := set $env "BIRGE_APNS_ENVIRONMENT" .apns.environment -}}
{{- end -}}
{{- end -}}
{{- if $v.courierHook.enabled -}}
{{- $_ := set $env "BIRGE_INTERNAL_ADDR" (printf ":%s" (include "birge.internalPort" .)) -}}
{{- end -}}
{{- end -}}
{{- range (include "birge.secretFiles" . | fromYamlArray) -}}
{{- if .env }}{{ $_ := set $env .env (printf "/secrets/%s" .file) }}{{ end -}}
{{- end -}}
{{- /* Free-form non-secret variables; the typed and secret-file values above win. */ -}}
{{- $extra := merge (dict) (get (index $v $c) "env" | default dict) ($v.config.env | default dict) -}}
{{- if hasKey $extra "DATABASE_URL" -}}
{{- fail "DATABASE_URL holds a password: set it through secrets.database, never in an env map" -}}
{{- end -}}
{{- $all := merge $env $extra -}}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ required "secrets.database.name" $v.secrets.database.name }}
      key: {{ required "secrets.database.key" $v.secrets.database.key }}
{{- include "birge.envMap" $all }}
{{- end -}}

{{/* Writable scratch space; the root filesystem is read-only. */}}
{{- define "birge.tmpVolume" -}}
- name: tmp
  emptyDir:
    sizeLimit: 64Mi
{{- end -}}
