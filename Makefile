# All CI checks are make targets so they run the same locally and in any CI.
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1
GO ?= go
GOLANGCI_LINT := $(CURDIR)/bin/golangci-lint
OAPI := $(GO) -C tools tool oapi-codegen
PSQLDEF := $(CURDIR)/bin/psqldef
# Go packages of this module. node_modules is excluded: some npm packages ship
# Go sources, and ./... would vet, test and lint them as part of the module.
GO_PKGS = $(shell $(GO) list -f '{{.Dir}}' ./... | grep -v /node_modules/)
# API area -> generated file; one oapi-codegen config per area in contracts/codegen/.
API_AREAS := meta:internal/platform/meta/api.gen.go membership:internal/domains/membership/api/api.gen.go privacy:internal/domains/privacy/api/api.gen.go notify:internal/domains/notify/api/api.gen.go safety:internal/domains/safety/api/api.gen.go wellbeing:internal/domains/wellbeing/api/api.gen.go emergency:internal/domains/emergency/api/api.gen.go assistant:internal/domains/assistant/api/api.gen.go knowledge:internal/domains/knowledge/api/api.gen.go circle:internal/domains/circle/api/api.gen.go chat:internal/domains/chat/api/api.gen.go sharing:internal/domains/sharing/api/api.gen.go directory:internal/domains/directory/api/api.gen.go moderation:internal/domains/moderation/api/api.gen.go analytics:internal/domains/analytics/api/api.gen.go staff:internal/platform/staffdir/api/api.gen.go

.PHONY: check secrets lint api-gen api-check psqldef migration db-check go-check go-fmt-check go-vet go-test go-build eval sqlc sqlc-check kb-export app-check

check: secrets go-check

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner

go-check: go-fmt-check sqlc-check api-check db-check lint go-vet go-test go-build eval

go-fmt-check:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

go-vet:
	$(GO) vet $(GO_PKGS)

go-test:
	$(GO) test -race $(GO_PKGS)

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
