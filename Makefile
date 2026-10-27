# All CI checks are make targets so they run the same locally and in any CI.
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1
GO ?= go

.PHONY: check secrets go-check go-fmt-check go-vet go-test go-build sqlc sqlc-check

check: secrets go-check

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner

go-check: go-fmt-check sqlc-check go-vet go-test go-build

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
