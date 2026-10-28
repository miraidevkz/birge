# Images of the birge binary (api, worker, migrate, keygen). Build context is
# the repository root; .dockerignore keeps the clients and node_modules out.
#
# Targets:
#   release  the production image the Helm chart runs (deploy/helm/birge):
#            only /usr/local/bin/birge on distroless static (no shell, no
#            package manager), user 10001. Build with `--target release`.
#   dev      (default, last stage) the birge binary plus the
#            development-only staff login app and SMS relay (birge-devlogin,
#            birge-devsms) on Alpine, with the shell the compose keygen
#            service needs. Never deploy it.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# timetzdata embeds the zone database so BIRGE_TIME_ZONE works on any base.
RUN CGO_ENABLED=0 go build -trimpath -tags timetzdata -o /out/birge ./cmd/birge

# The development-only binaries in a stage of their own: `--target release`
# never builds them.
FROM build AS build-dev
RUN CGO_ENABLED=0 go build -trimpath -o /out/birge-devlogin ./cmd/birge-devlogin
RUN CGO_ENABLED=0 go build -trimpath -o /out/birge-devsms ./cmd/birge-devsms

# Distroless static (Apache-2.0), pinned by digest: CA certificates and
# /etc/passwd only. The binary is static (CGO_ENABLED=0) and embeds tzdata.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS release
COPY --from=build /out/birge /usr/local/bin/birge
# The same numeric user as the dev image and the chart's securityContext.
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/birge"]

# Last stage, so a build without --target (the compose stack) gets it.
FROM alpine:3.22 AS dev
RUN adduser -D -u 10001 birge
COPY --from=build-dev /out/birge /out/birge-devlogin /out/birge-devsms /usr/local/bin/
USER birge
ENTRYPOINT ["birge"]
