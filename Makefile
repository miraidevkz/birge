# All CI checks are make targets so they run the same locally and in any CI.
GITLEAKS_IMAGE := ghcr.io/gitleaks/gitleaks:v8.30.1

.PHONY: check secrets

check: secrets

secrets:
	docker run --rm -v "$(CURDIR):/repo:ro" $(GITLEAKS_IMAGE) git /repo --redact --no-banner
