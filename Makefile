# All CI checks are make targets so they run the same locally and in any CI.
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1
GO ?= go
GOLANGCI_LINT := $(CURDIR)/bin/golangci-lint
OAPI := $(GO) -C tools tool oapi-codegen
PSQLDEF := $(CURDIR)/bin/psqldef
# CI-only tools, pinned in their own module (tools/ci/go.mod) so they never
# move the dependencies of golangci-lint and oapi-codegen in tools/go.mod.
CI_TOOL := $(GO) -C tools/ci
GOVULNCHECK := $(CURDIR)/bin/govulncheck
OASDIFF := $(CURDIR)/bin/oasdiff
ACTIONLINT := $(CURDIR)/bin/actionlint
# Container tools run as pinned images, like gitleaks: nothing to install.
SYFT_IMAGE := docker.io/anchore/syft:v1.55.0@sha256:4cc92b64ca74ffca4c94d4c8fcc01f217731375300be14d0b6eeb2e82eaa110a
COSIGN_IMAGE := ghcr.io/sigstore/cosign/cosign:v3.1.4@sha256:0f06154836d2de34f887a5c273506d52ca43edaa1d9e53e6510fa78a48f69c2d
# Go packages of this module. node_modules is excluded: some npm packages ship
# Go sources, and ./... would vet, test and lint them as part of the module.
GO_PKGS = $(shell $(GO) list -f '{{.Dir}}' ./... | grep -v /node_modules/)
# API area -> generated file; one oapi-codegen config per area in contracts/codegen/.
API_AREAS := meta:internal/platform/meta/api.gen.go membership:internal/domains/membership/api/api.gen.go privacy:internal/domains/privacy/api/api.gen.go notify:internal/domains/notify/api/api.gen.go safety:internal/domains/safety/api/api.gen.go wellbeing:internal/domains/wellbeing/api/api.gen.go emergency:internal/domains/emergency/api/api.gen.go assistant:internal/domains/assistant/api/api.gen.go knowledge:internal/domains/knowledge/api/api.gen.go circle:internal/domains/circle/api/api.gen.go chat:internal/domains/chat/api/api.gen.go sharing:internal/domains/sharing/api/api.gen.go directory:internal/domains/directory/api/api.gen.go moderation:internal/domains/moderation/api/api.gen.go analytics:internal/domains/analytics/api/api.gen.go staff:internal/platform/staffdir/api/api.gen.go

.PHONY: check docs-check helm-check secrets lint api-gen api-check psqldef migration db-check go-check go-fmt-check go-vet go-test go-build eval sqlc sqlc-check kb-export app-check \
	vuln contract-diff coverage-check coverage-floor workflow-lint image sbom image-sign helm-lint

check: secrets go-check

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner

# Relative links and heading anchors in every Markdown file (also part of
# go-test). Its own target so the docs workflow can run it alone.
docs-check:
	$(GO) test ./internal/docstest/

go-check: go-fmt-check sqlc-check api-check db-check lint go-vet go-test coverage-check go-build eval

go-fmt-check:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

go-vet:
	$(GO) vet $(GO_PKGS)

# One test run feeds the coverage ratchet: the profile is written here and
# read by coverage-check, so the tests never run twice in go-check.
COVER_OUT ?= $(CURDIR)/bin/coverage.out
go-test:
	@mkdir -p $(dir $(COVER_OUT))
	$(GO) test -race -coverprofile=$(COVER_OUT) $(GO_PKGS)

# Coverage ratchet: per-context statement coverage (internal/domains/<context>
# and internal/platform, generated files excluded) against coverage-floor.txt;
# fails on a drop of more than 0.5 points. coverage-floor raises the floors.
coverage-check: go-test
	$(GO) run ./cmd/birge-coverage -profile $(COVER_OUT) -floor coverage-floor.txt

coverage-floor: go-test
	$(GO) run ./cmd/birge-coverage -profile $(COVER_OUT) -floor coverage-floor.txt -update

# Known vulnerabilities in the module's dependencies and the Go standard
# library that the code actually reaches. VULN_DB can point at a mirror
# (file:///path) for a deployment that must not call vuln.go.dev.
VULN_DB ?= https://vuln.go.dev
vuln:
	$(CI_TOOL) build -o $(GOVULNCHECK) golang.org/x/vuln/cmd/govulncheck
	$(GOVULNCHECK) -db $(VULN_DB) $(GO_PKGS)

# ADR-0018: no breaking change against any released contract snapshot
# (oasdiff exits 1 on a breaking change, 102 on a spec it cannot load). The
# fixtures in tools/ci/testdata/contract-diff prove first that oasdiff still
# catches a breaking change and accepts an additive one, and birge.yaml is
# compared with itself so a spec oasdiff cannot load fails before a snapshot
# of it exists.
CONTRACT_FIXTURES := tools/ci/testdata/contract-diff
contract-diff:
	@$(CI_TOOL) build -o $(OASDIFF) github.com/oasdiff/oasdiff
	@for f in $(CONTRACT_FIXTURES)/breaking-*.yaml; do \
	  $(OASDIFF) breaking --fail-on ERR $(CONTRACT_FIXTURES)/base.yaml $$f >/dev/null 2>&1; s=$$?; \
	  [ $$s = 1 ] || { echo "contract-diff self-test: $$f gave exit $$s, want 1 (breaking)"; exit 1; }; done
	@for f in $(CONTRACT_FIXTURES)/compatible-*.yaml; do \
	  $(OASDIFF) breaking --fail-on ERR $(CONTRACT_FIXTURES)/base.yaml $$f || { \
	    echo "contract-diff self-test: $$f was reported as breaking"; exit 1; }; done
	@$(OASDIFF) breaking --fail-on ERR contracts/openapi/birge.yaml contracts/openapi/birge.yaml
	@n=0; for f in contracts/openapi/released/*.yaml; do [ -e "$$f" ] || continue; n=$$((n+1)); \
	  echo "contract-diff: $$f -> contracts/openapi/birge.yaml"; \
	  $(OASDIFF) breaking --fail-on ERR $$f contracts/openapi/birge.yaml || exit 1; done; \
	  echo "contract-diff: self-test passed, $$n released snapshot(s) checked"

# Lints .github/workflows (syntax, expressions, action inputs, shellcheck when
# it is installed).
workflow-lint:
	$(CI_TOOL) build -o $(ACTIONLINT) github.com/rhysd/actionlint/cmd/actionlint
	$(ACTIONLINT) .github/workflows/*.yml

# Image of cmd/birge (deploy/compose/Dockerfile), its SPDX SBOM, and the
# key-based signature. image and sbom work offline of any registry; image-sign
# needs IMAGE_REF (a pushed image by digest), COSIGN_PRIVATE_KEY and
# COSIGN_PASSWORD, and never uses Fulcio or Rekor (ADR-0012).
IMAGE ?= birge:dev
DOCKER_BUILD ?= docker build
SBOM_OUT ?= $(CURDIR)/bin/sbom.spdx.json
image:
	$(DOCKER_BUILD) -f deploy/compose/Dockerfile --target release -t $(IMAGE) .

# syft reads a saved archive with no network: no Docker socket, no update check.
sbom:
	@mkdir -p $(dir $(SBOM_OUT))
	@t=$$(mktemp -d); trap 'rm -rf $$t' EXIT; \
	  docker save $(IMAGE) -o $$t/image.tar && chmod 0755 $$t && chmod 0644 $$t/image.tar && \
	  docker run --rm --network none -e SYFT_CHECK_FOR_APP_UPDATE=false -v "$$t:/in:ro" $(SYFT_IMAGE) scan docker-archive:/in/image.tar -o spdx-json --quiet > $(SBOM_OUT).tmp && \
	  mv $(SBOM_OUT).tmp $(SBOM_OUT); s=$$?; rm -f $(SBOM_OUT).tmp; exit $$s
	@echo "SBOM written to $(SBOM_OUT)"

# Registry credentials come from the caller's Docker config (docker login),
# read as the caller's user; the key and its password only through the
# environment, never a file or an argument. --use-signing-config=false and
# --tlog-upload=false keep cosign off the public Sigstore services: no TUF
# fetch, no Fulcio certificate, no Rekor entry. Verify with
# cosign verify --key cosign.pub --insecure-ignore-tlog --private-infrastructure.
COSIGN_RUN = docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp -e COSIGN_PRIVATE_KEY -e COSIGN_PASSWORD \
	-e DOCKER_CONFIG=/docker -v "$${DOCKER_CONFIG:-$$HOME/.docker}:/docker:ro" -v "$(dir $(SBOM_OUT)):/sbom:ro" $(COSIGN_IMAGE)
COSIGN_KEY_FLAGS = --yes --key env://COSIGN_PRIVATE_KEY --tlog-upload=false --use-signing-config=false
image-sign:
	@test -n "$(IMAGE_REF)" || { echo "IMAGE_REF (registry/name@sha256:...) is required"; exit 2; }
	@test -n "$$COSIGN_PRIVATE_KEY" || { echo "COSIGN_PRIVATE_KEY is not set"; exit 2; }
	@test -f "$(SBOM_OUT)" || { echo "$(SBOM_OUT) is missing: run make sbom first"; exit 2; }
	$(COSIGN_RUN) sign $(COSIGN_KEY_FLAGS) $(IMAGE_REF)
	$(COSIGN_RUN) attest $(COSIGN_KEY_FLAGS) --type spdxjson --predicate /sbom/$(notdir $(SBOM_OUT)) $(IMAGE_REF)

# Helm charts under deploy/helm (ADR-0011): CI's helm job. It runs the full
# helm-check (pinned helm and kubeconform from bin/), and passes while there
# is no chart.
helm-lint:
	@if [ ! -d deploy/helm ]; then echo "helm-lint: no deploy/helm yet, skipped"; exit 0; fi; \
	  $(MAKE) --no-print-directory helm-check

go-build:
	CGO_ENABLED=0 $(GO) build -trimpath -o /dev/null ./cmd/birge

# AI regression sets of ADR-0017 (crisis, anonymization, llm-down) through the
# real assistant pipeline with in-memory storage and fake providers: offline,
# no database, no network, no LLM. Writes the JSON report to EVAL_OUT; with
# EVAL_BASELINE (a report from main) any drop fails. Live-model runs are
# manual only (docs/development.md, "AI evaluation").
EVAL_OUT ?= $(CURDIR)/bin/eval-report.json
EVAL_BASELINE ?=
eval:
	@mkdir -p $(dir $(EVAL_OUT))
	$(GO) run ./cmd/birge-eval -out $(EVAL_OUT) -commit "$$(git rev-parse HEAD 2>/dev/null || echo unknown)" $(if $(EVAL_BASELINE),-baseline $(EVAL_BASELINE))

# Export published knowledge versions to knowledge/{kk,ru,en}/ (ADR-0022).
# Requires DATABASE_URL pointing at a database that has been migrated (and
# typically seeded). Overwrites matching files; deterministic when content
# is unchanged.
kb-export:
	$(GO) run ./cmd/birge kb-export --out knowledge

sqlc:
	$(GO) -C tools tool sqlc -f ../sqlc.yaml generate

sqlc-check:
	$(GO) -C tools tool sqlc -f ../sqlc.yaml diff

lint:
	$(GO) -C tools build -o $(GOLANGCI_LINT) github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	$(GOLANGCI_LINT) run $(GO_PKGS)

api-gen:
	@for a in $(API_AREAS); do n=$${a%%:*}; f=$${a#*:}; \
	  $(OAPI) -config ../contracts/codegen/$$n.yaml -o ../$$f ../contracts/openapi/birge.yaml || exit 1; done

api-check:
	@for a in $(API_AREAS); do n=$${a%%:*}; f=$${a#*:}; t=$$(mktemp); \
	  $(OAPI) -config ../contracts/codegen/$$n.yaml -o $$t ../contracts/openapi/birge.yaml || exit 1; \
	  cmp -s $$t $$f || { echo "$$f is stale: run make api-gen"; rm -f $$t; exit 1; }; rm -f $$t; done

psqldef:
	@$(GO) -C tools build -o $(PSQLDEF) github.com/sqldef/sqldef/v3/cmd/psqldef

# Prints a new migration to stdout; save it under db/migrations/ with the printed name.
migration: psqldef
	@$(GO) run ./cmd/birge-dbtool -psqldef $(PSQLDEF) migration $(name)

db-check: psqldef
	@$(GO) run ./cmd/birge-dbtool -psqldef $(PSQLDEF) check

# Client checks (apps/mobile and packages/*): typecheck, lint, tests, the
# generated API client and the web build. Needs Node and `npm ci` first.
app-check:
	npm run check

# Helm chart checks (deploy/helm/birge): helm lint and helm template with the
# default, pilot and all-features values; the rendered manifests validated by
# kubeconform against pinned Kubernetes and CRD schemas; and guards for the
# chart's own rules: no Secret object (secrets are references only, AGENTS.md
# non-negotiable 4), no Ingress backend on a metrics or internal port, and the
# PrometheusRule copy of the alerts equal to deploy/prometheus/alerts.yml.
# helm (Apache-2.0) and kubeconform (Apache-2.0) are release binaries
# downloaded into bin/ and checked against the SHA-256 pinned here; schemas
# are fetched from pinned commits. Needs curl and network on first run.
HELM_VERSION := v4.3.1
HELM_SHA256_linux-amd64 := 0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b
HELM_SHA256_linux-arm64 := 8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88
HELM_SHA256_darwin-arm64 := 6b4e61c8cd27d99e5a801e63ff25dd5c70b36875c2cd659d3e9ce6b76fc23198
KUBECONFORM_VERSION := v0.8.0
KUBECONFORM_SHA256_linux-amd64 := 9bc2bffbf71f261128533edaf912153948b7ff238f9a531ae6d34466ec287883
KUBECONFORM_SHA256_linux-arm64 := 1f53fc8e81258197a35e8603054162a5af1de8c5af13746c71ab680d9534ed87
KUBECONFORM_SHA256_darwin-arm64 := f84f4dfbebf4a6b0b230385fa065a39ea35e02608c2b50d025dcf64775a69d67
# Kubernetes of current k3s; schemas from yannh/kubernetes-json-schema and
# datreeio/CRDs-catalog (Traefik, cert-manager, Prometheus Operator) at fixed commits.
HELM_KUBE_VERSION := 1.34.0
K8S_SCHEMAS := https://raw.githubusercontent.com/yannh/kubernetes-json-schema/89a35a5e9a1412a692a2603d79c3786f602238a4/{{.NormalizedKubernetesVersion}}-standalone-strict/{{.ResourceKind}}{{.KindSuffix}}.json
CRD_SCHEMAS := https://raw.githubusercontent.com/datreeio/CRDs-catalog/3eb90d47aba8d61919d4419dd38301536e32a94b/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json
TOOLS_PLATFORM := $(shell uname -s | tr A-Z a-z)-$(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
HELM := $(CURDIR)/bin/helm-$(HELM_VERSION)
KUBECONFORM := $(CURDIR)/bin/kubeconform-$(KUBECONFORM_VERSION)
HELM_CHART := deploy/helm/birge
HELM_VALUES := default: pilot:$(HELM_CHART)/values-pilot.yaml all:$(HELM_CHART)/ci/all-features-values.yaml
# helm keeps no state outside bin/.
HELM_ENV := HELM_CACHE_HOME=$(CURDIR)/bin/helm-home/cache HELM_CONFIG_HOME=$(CURDIR)/bin/helm-home/config HELM_DATA_HOME=$(CURDIR)/bin/helm-home/data

# fetch-verified <url> <sha256> <archive member> <target>
define fetch-verified
	@test -n "$(2)" || { echo "no pinned checksum for $(TOOLS_PLATFORM)"; exit 1; }
	@mkdir -p $(CURDIR)/bin
	@d=$$(mktemp -d) && trap 'rm -rf "$$d"' EXIT && \
	curl -fsSL -o "$$d/a.tgz" "$(1)" && \
	if command -v sha256sum >/dev/null; then got=$$(sha256sum "$$d/a.tgz"); else got=$$(shasum -a 256 "$$d/a.tgz"); fi && \
	test "$${got%% *}" = "$(2)" || { echo "checksum mismatch for $(1)"; exit 1; }; \
	tar -xzf "$$d/a.tgz" -C "$$d" "$(3)" && mv "$$d/$(3)" "$(4)" && chmod 0755 "$(4)"
endef

$(HELM):
	$(call fetch-verified,https://get.helm.sh/helm-$(HELM_VERSION)-$(TOOLS_PLATFORM).tar.gz,$(HELM_SHA256_$(TOOLS_PLATFORM)),$(TOOLS_PLATFORM)/helm,$@)

$(KUBECONFORM):
	$(call fetch-verified,https://github.com/yannh/kubeconform/releases/download/$(KUBECONFORM_VERSION)/kubeconform-$(TOOLS_PLATFORM).tar.gz,$(KUBECONFORM_SHA256_$(TOOLS_PLATFORM)),kubeconform,$@)

helm-check: $(HELM) $(KUBECONFORM)
	@cmp -s deploy/prometheus/alerts.yml $(HELM_CHART)/files/alerts.yml || { echo "$(HELM_CHART)/files/alerts.yml differs from deploy/prometheus/alerts.yml: copy it"; exit 1; }
	@set -e; out=$$(mktemp -d); trap 'rm -rf "$$out"' EXIT; \
	for v in $(HELM_VALUES); do n=$${v%%:*}; f=$${v#*:}; \
	  echo "== $$n values"; \
	  $(HELM_ENV) $(HELM) lint --strict $(HELM_CHART) $${f:+-f $$f}; \
	  $(HELM_ENV) $(HELM) template birge $(HELM_CHART) --namespace birge --kube-version $(HELM_KUBE_VERSION) $${f:+-f $$f} >"$$out/$$n.yaml"; \
	  if grep -Eq '^kind: Secret$$' "$$out/$$n.yaml"; then echo "$$n: the chart renders a Secret; secrets must be references"; exit 1; fi; \
	  awk '/^---/{if(k&&b)bad=1;k=0;b=0;next} /^kind: Ingress$$/{k=1} /name: [a-z0-9-]*(-metrics|-worker-internal|metrics|internal)$$/{b=1} END{if(k&&b)bad=1;exit bad}' "$$out/$$n.yaml" || { echo "$$n: an Ingress routes a metrics or internal port"; exit 1; }; \
	  $(KUBECONFORM) -strict -summary -kubernetes-version $(HELM_KUBE_VERSION) \
	    -schema-location '$(K8S_SCHEMAS)' -schema-location '$(CRD_SCHEMAS)' "$$out/$$n.yaml"; \
	done
