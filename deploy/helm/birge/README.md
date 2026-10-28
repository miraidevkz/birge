# Birge Helm chart

Deploys Birge on Kubernetes (ADR-0011); a single-node pilot runs the same chart on k3s. The chart holds the Birge components only:

| Component | Kind | Notes |
|---|---|---|
| API (`birge api`) | Deployment, 2 replicas, PodDisruptionBudget | port `http` 8080 behind the ingress; `metrics` 9090 on a ClusterIP Service |
| Worker (`birge worker`) | Deployment, 1 replica | `http` 8080 for probes only; `internal` 8090 (Kratos courier hook) on a ClusterIP Service reachable from Kratos only; `metrics` 9090 |
| Migrations (`birge migrate`) | Job, `pre-install,pre-upgrade` hook | Helm applies the Deployments only after it succeeds |
| Member web, staff admin web | Deployment + Service each | the Expo web exports, served by Caddy with the headers of `deploy/compose/Caddyfile` |
| Ingress | Ingress (Traefik), Traefik Middlewares | routing of `deploy/compose/Caddyfile` |
| NetworkPolicies | deny by default, then one policy per allowed path | |
| Monitoring | metrics Services, optional ServiceMonitor and PrometheusRule | |

Everything else (PostgreSQL, NATS, Kratos, Hydra, object storage, retrieval, OpenBao, observability) is installed from its own upstream chart or operator and is documented below, not vendored.

`make helm-check` lints and renders the chart with the default, pilot and all-features values and validates the output with kubeconform (see the Makefile).

## Why one chart

The API, worker, migration job and web clients share one configuration, one set of keys and one release lifecycle: the migrate hook must finish before *both* the API and the worker roll, and Helm orders hooks only within a release. Separate charts would need an external orchestrator for that ordering and would duplicate the configuration and secret references. Each component can still be switched off (`web.member.enabled`, `web.admin.enabled`, `ingress.enabled`, `metrics.*`).

## Images

- **birge** (`image`): the `release` target of `deploy/compose/Dockerfile`, distroless static with only `/usr/local/bin/birge`, user 10001:

  ```sh
  docker build -f deploy/compose/Dockerfile --target release -t <registry>/birge:<version> .
  ```

  Never deploy the default `dev` target: it carries `birge-devlogin` (signs anyone in as any staff role) and `birge-devsms`.
- **Web clients** (`web.member.image`, `web.admin.image`): Caddy 2 with the export in `/srv`. The chart supplies the Caddyfile (admin API off, no automatic HTTPS, `/healthz`, single-page fallback, and for the member app `Cache-Control: no-store` and `Referrer-Policy: no-referrer` on `/plan` and `/sos-link`). The official Caddy image gives `/usr/bin/caddy` the `cap_net_bind_service` file capability, which a pod with every capability dropped cannot execute, so remove it (the chart listens on 8080):

  ```dockerfile
  FROM caddy:2.10.2-alpine
  RUN apk add --no-cache libcap && setcap -r /usr/bin/caddy && apk del libcap
  COPY dist/ /srv/
  ```

  The export bakes the public URLs in. Member app: `EXPO_PUBLIC_API_URL=https://<member host>/v1 EXPO_PUBLIC_KRATOS_URL=https://<member host>/auth npx expo export --platform web` in `apps/mobile` (no `EXPO_PUBLIC_DEMO_CODES_URL` in production). Admin: `EXPO_PUBLIC_API_URL=https://<admin host>/v1`, `EXPO_PUBLIC_STAFF_ISSUER` and `EXPO_PUBLIC_STAFF_CLIENT_ID` of the staff OIDC provider, in `apps/admin`.

## Secrets

The chart never holds a secret value, not even a sample (AGENTS.md, non-negotiable 4): values name a Secret and a key, and the pods mount them as files under `/secrets` (projected volume, mode 0440, group = `fsGroup`) with the matching `*_FILE` variable pointing there. `DATABASE_URL` has no file variable, so it is the one secret passed as an environment variable through `secretKeyRef`. A Secret that does not exist keeps the pod in `ContainerCreating`, so a missing key shows at deploy time.

Create them in the release namespace from the secret store (OpenBao, through the External Secrets Operator or the OpenBao agent; or by hand with `kubectl create secret generic` from files that never touch the repository). Keys are text, at least 32 bytes after trailing whitespace is removed.

| Secret (default name) | Keys | Used by | How to make it |
|---|---|---|---|
| `birge-db-app` | `uri` | API, worker (`DATABASE_URL`) | `postgres://birge_app:<password>@birge-db-rw:5432/birge?sslmode=require`; the role and its password come from the CloudNativePG managed role |
| `birge-db-migrator` | `uri` | migrate job (`DATABASE_URL`) | the same for `birge_migrator` |
| `birge-keys` | `kek.json`, `pow.key`, `hash.key`, `plan-link.key`, `analytics.key` (and `blind-index.key`, unused) | API: kek, pow, hash, plan-link. Worker: kek, hash, plan-link, analytics | `birge keygen -dir <empty dir on an offline machine>` writes exactly these file names (it prints `BIRGE_KEK_ID=local-1`; choose your own id in `config.kekId`, it only has to stay with this keyset). Then `kubectl create secret generic birge-keys --from-file=<dir>` and shred the directory. `hash.key`, `pow.key`, `plan-link.key` and `analytics.key` must all differ (keygen makes them so) |
| `birge-courier-hook` | `secret` | worker (`BIRGE_COURIER_SECRET_FILE`) and Kratos | `openssl rand -hex 32` |
| `birge-staff-oidc` | `client-secret` | API (`BIRGE_STAFF_CLIENT_SECRET_FILE`), with `staff.enabled` | the secret of the confidential client the API introspects as, registered in Hydra |
| `birge-push` | `fcm-service-account.json`, `apns-key.p8` | worker, for `fcm` / `apns` | Firebase service-account JSON key; APNs token-auth key |
| `birge-smsc` | `login`, `password` | worker, with `smsc` | the SMSC.kz account |
| `birge-s3` | `access-key`, `secret-key` | API, worker, with `config.s3.enabled` | a Garage key with access to the photo bucket only |
| `birge-llm` (optional) | `api-key` | API (`BIRGE_LLM_API_KEY_FILE`) when `secrets.llmApiKey.name` is set | the LLM provider key; without it the assistant answers degraded and the crisis path is unaffected |
| `birge-kek-<id>` (rotation only) | `kek.json` | API, worker (`BIRGE_KEK_PREVIOUS_KEYSET_FILES`) via `secrets.previousKeks` | see docs/development.md, "KEK rotation" |
| `birge-member-tls`, `birge-admin-tls` | `tls.crt`, `tls.key` | Traefik | an existing certificate, or filled by cert-manager with `ingress.certManager` |

Production keys are made once and live in the secret store; the KEK moves to OpenBao Transit per ADR-0009 later. Never commit the keygen output or paste a key into a values file.

## Dependencies and install order (pilot)

The pilot is one k3s node in Kazakhstan; every component and its storage stays there (ADR-0013). Pin every upstream chart version in your own install notes; all are FOSS (ADR-0012). The default values assume the dependencies run in the release namespace (`birge`) with the service names and pod labels below; override `config.*` URLs and `networkPolicy.*` selectors if yours differ.

1. **k3s** with its bundled Traefik (namespace `kube-system`, used by `ingress.className: traefik` and the default NetworkPolicy selectors) and a NetworkPolicy-enforcing CNI (k3s' default flannel + kube-router netpol controller enforces them). Redirect HTTP to HTTPS in Traefik's `websecure` entry point configuration. Optionally **cert-manager** with a ClusterIssuer (`ingress.certManager`).
2. **OpenBao** (MPL-2.0; HashiCorp Vault is rejected under ADR-0012) and a way to sync its secrets into Kubernetes Secrets (External Secrets Operator, Apache-2.0). Then create the Secrets in the table above.
3. **CloudNativePG** operator, then a `Cluster` named `birge-db` (pods carry `cnpg.io/cluster: birge-db`, service `birge-db-rw`) with PostgreSQL 18, databases `birge`, `kratos` and `hydra`, and managed roles `birge_migrator` (owner of the `birge` database) and `birge_app` (login, no DDL; `db/roles.sql` grants it the rest at every migrate). Backups (`barmanObjectStore`) go to a separate Garage bucket with its own credentials (ADR-0015).
4. **Garage** (object storage, AGPL-3.0): buckets for photos and practices, and a separate one for database backups. Pod label `app.kubernetes.io/name: garage`, S3 API on port 3900. Enable `networkPolicy.egress.objectStorage`.
5. **NATS** (official chart) with JetStream enabled and a persistent volume; service `nats:4222`. Birge creates its stream itself.
6. **Kratos** (Ory chart), see below; then **Hydra** (Ory chart) for staff, see below.
7. **TEI** (text-embeddings-inference) serving the embedding model and **Qdrant** (service `qdrant:6333`) for retrieval; optional (without them the assistant answers without knowledge fragments). Enable `networkPolicy.egress.tei` and `.qdrant`, then run `birge kb-reindex` once as a Job after the first install.
8. **Observability** (ADR-0021): kube-prometheus-stack or Prometheus Operator in namespace `monitoring` (labels in `networkPolicy.prometheus`), Alertmanager with its webhook to Birge notify, Loki, Tempo with an OpenTelemetry collector (`config.otlpEndpoint`), Grafana, GlitchTip. All in-cluster.
9. **Birge**:

   ```sh
   helm upgrade --install birge deploy/helm/birge -n birge \
     -f deploy/helm/birge/values-pilot.yaml -f <site overrides>
   ```

   The migrate hook runs first (it needs `birge-db-migrator`); the API and the worker start only after it succeeds. On a failed migration the release stops, the previous pods keep serving, and the Job stays for `kubectl logs job/birge-migrate`.

### Kratos

Members sign in through the API's `/auth/` gateway; Kratos is never exposed by the ingress. In the Ory Kratos chart values:

- `kratos.identitySchemas`: `identity.schema.json` from `deploy/compose/kratos/` (phone + SMS code), as the `member` schema.
- `kratos.config`: `deploy/compose/kratos/kratos.yml` without `--dev`, with `serve.public.base_url: https://<member host>/auth/`, flows of 10 minutes, the `courier.templates` as they are (SMS carry only the code), and no `courier.channels` (set below). DSN and the cookie and cipher secrets from a Secret through the chart's secret settings.
- The courier hook of `deploy/compose/kratos/courier-hook.yml`: Kratos reads configuration arrays from JSON in environment variables, and Kubernetes expands `$(VAR)` from an earlier variable, so the bearer secret comes from the same `birge-courier-hook` Secret the worker reads and never appears in a values file:

  ```yaml
  deployment:
    extraEnv:
      - name: BIRGE_COURIER_SECRET
        valueFrom: {secretKeyRef: {name: birge-courier-hook, key: secret}}
      - name: COURIER_CHANNELS
        value: '[{"id":"sms","type":"http","request_config":{"url":"http://birge-worker-internal.birge.svc:8090/internal/kratos/courier","method":"POST","body":"file:///etc/config/kratos/courier-hook.jsonnet","auth":{"type":"api_key","config":{"in":"header","name":"Authorization","value":"Bearer $(BIRGE_COURIER_SECRET)"}}}}]'
    extraVolumes: [{name: courier-body, configMap: {name: kratos-courier-body}}]
    extraVolumeMounts: [{name: courier-body, mountPath: /etc/config/kratos/courier-hook.jsonnet, subPath: courier-hook.jsonnet, readOnly: true}]
  courier:
    enabled: true
  ```

  with a ConfigMap `kratos-courier-body` made from `deploy/compose/kratos/courier-hook.jsonnet`. Run the courier (`courier.enabled` or `--watch-courier`). Kratos pods carry `app.kubernetes.io/name: kratos`, which `networkPolicy.kratos` admits to the worker's internal port and nothing else. Services `kratos-public` and `kratos-admin` match `config.kratosPublicUrl` and `config.kratosAdminUrl`.

### Hydra

Staff sign in at Hydra with WebAuthn (ADR-0008); the development login app (`birge-devlogin`) is never deployed, so a login and consent app that performs WebAuthn is required before `staff.enabled`. Register the admin web app as a public client with PKCE (as `deploy/compose/hydra/admin-client.json`, with the admin host as redirect URI) and a confidential client for the API (`staff.clientId`, secret in `birge-staff-oidc`). The API reaches Hydra's admin port 4445 for introspection only (`networkPolicy.egress.hydra`). Hydra's public endpoint (the issuer) is published by its own ingress, reachable from the staff VPN; the chart refuses `staff.mfaAmr=dev`.

### Prometheus

The API and the worker serve `GET /metrics` on port 9090 through `<release>-api-metrics` and `<release>-worker-metrics`. The alert rules expect the job names `birge-api` and `birge-worker`.

- With the Prometheus Operator: `metrics.serviceMonitor.enabled` (the job name comes from the Service label `birge.scrape/job`) and `metrics.prometheusRule.enabled`, which loads `files/alerts.yml`, a copy of `deploy/prometheus/alerts.yml` (`make helm-check` fails when they differ; edit the original and copy it).
- Without it: keep `metrics.scrapeAnnotations`, load `deploy/prometheus/alerts.yml` as a rule file, and set the job from the label:

  ```yaml
  - job_name: birge
    kubernetes_sd_configs: [{role: endpoints, namespaces: {names: [birge]}}]
    relabel_configs:
      - {source_labels: [__meta_kubernetes_service_label_birge_scrape_job], regex: (.+), action: keep}
      - {source_labels: [__meta_kubernetes_service_label_birge_scrape_job], target_label: job}
  ```

The external probe of the public URL (blackbox exporter) runs outside the cluster.

## Network policies

`<release>-default-deny` selects every pod of the release with no rule, so nothing is allowed until another policy allows it:

- ingress controller → API `http`, member and admin web `http`;
- Kratos → worker `internal`;
- Prometheus → API and worker `metrics`;
- API, worker, migrate → DNS and the dependencies in `networkPolicy.egress` that list them (`components`). `external` (HTTPS outside private ranges) serves the LLM from the API and FCM, APNs and SMSC from the worker; narrow it to the providers' ranges where they are stable.

The web pods have no egress. Probes come from the node and are not affected.

## Pilot values

`values-pilot.yaml`: production delivery (FCM, APNs, SMSC), `BIRGE_REFERENCE_PRESETS=kz`, rate limits in PostgreSQL (two API replicas), retrieval and object storage on, ServiceMonitor and PrometheusRule, the admin ingress limited to the staff VPN, cert-manager TLS. Its hosts, IDs and the VPN range are placeholders. Before members sign up, replace every DEMO preset (crisis lexicon and phones, questionnaire, consent versions, SMS texts; docs/development.md, "Demo presets"): non-secret values go to `config.env`, files to `api.extraVolumes` and `worker.extraVolumes` from ConfigMaps.

Resource requests and limits fit a single node with 4 CPUs and 8 GiB alongside the dependencies; raise them from real usage.
