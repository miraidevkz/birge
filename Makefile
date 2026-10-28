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

.PHONY: check secrets lint api-gen api-check psqldef migration db-check go-check go-fmt-check go-vet go-test go-build eval sqlc sqlc-check kb-export app-check \
	vuln contract-diff coverage-check coverage-floor workflow-lint image sbom image-sign helm-lint

check: secrets go-check

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner

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
	$(DOCKER_BUILD) -f deploy/compose/Dockerfile -t $(IMAGE) .

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

# Helm charts under deploy/helm (ADR-0011). Nothing to lint until they land;
# the target and the CI job then start checking them without a change here.
HELM ?= helm
helm-lint:
	@if [ ! -d deploy/helm ]; then echo "helm-lint: no deploy/helm yet, skipped"; exit 0; fi; \
	  found=0; for c in $$(find deploy/helm -name Chart.yaml -exec dirname {} \;); do found=1; \
	  $(HELM) lint --strict $$c || exit 1; done; \
	  [ $$found = 1 ] || { echo "helm-lint: deploy/helm has no Chart.yaml"; exit 1; }

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
