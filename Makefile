# All CI checks are make targets so they run the same locally and in any CI.
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1
GO ?= go
GOLANGCI_LINT := $(CURDIR)/bin/golangci-lint
OAPI := $(GO) -C tools tool oapi-codegen
# API area -> generated file; one oapi-codegen config per area in contracts/codegen/.
API_AREAS := meta:internal/platform/meta/api.gen.go

.PHONY: check secrets lint api-gen api-check go-check go-fmt-check go-vet go-test go-build sqlc sqlc-check

check: secrets go-check

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner

go-check: go-fmt-check sqlc-check api-check lint go-vet go-test go-build

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
