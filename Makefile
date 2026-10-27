# All CI checks are make targets so they run the same locally and in any CI.
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1
GO ?= go
GOLANGCI_LINT := $(CURDIR)/bin/golangci-lint
OAPI := $(GO) -C tools tool oapi-codegen
PSQLDEF := $(CURDIR)/bin/psqldef
# API area -> generated file; one oapi-codegen config per area in contracts/codegen/.
API_AREAS := meta:internal/platform/meta/api.gen.go membership:internal/domains/membership/api/api.gen.go privacy:internal/domains/privacy/api/api.gen.go notify:internal/domains/notify/api/api.gen.go safety:internal/domains/safety/api/api.gen.go wellbeing:internal/domains/wellbeing/api/api.gen.go emergency:internal/domains/emergency/api/api.gen.go assistant:internal/domains/assistant/api/api.gen.go knowledge:internal/domains/knowledge/api/api.gen.go

.PHONY: check secrets lint api-gen api-check psqldef migration db-check go-check go-fmt-check go-vet go-test go-build sqlc sqlc-check

check: secrets go-check

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner

go-check: go-fmt-check sqlc-check api-check db-check lint go-vet go-test go-build

go-fmt-check:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

go-vet:
	$(GO) vet ./...

go-test:
	$(GO) test -race ./...

go-build:
	CGO_ENABLED=0 $(GO) build -trimpath -o /dev/null ./cmd/birge

sqlc:
	$(GO) -C tools tool sqlc -f ../sqlc.yaml generate

sqlc-check:
	$(GO) -C tools tool sqlc -f ../sqlc.yaml diff

lint:
	$(GO) -C tools build -o $(GOLANGCI_LINT) github.com/golangci/golangci-lint/v2/cmd/golangci-lint
	$(GOLANGCI_LINT) run ./...

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
