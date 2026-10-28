# Image of the birge binary (api, worker, migrate, keygen) and the
# development-only staff login app and SMS relay (birge-devlogin,
# birge-devsms). Build context is
# the repository root; .dockerignore keeps the clients and node_modules out.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# timetzdata embeds the zone database so BIRGE_TIME_ZONE works on any base.
RUN CGO_ENABLED=0 go build -trimpath -tags timetzdata -o /out/birge ./cmd/birge
RUN CGO_ENABLED=0 go build -trimpath -o /out/birge-devlogin ./cmd/birge-devlogin
RUN CGO_ENABLED=0 go build -trimpath -o /out/birge-devsms ./cmd/birge-devsms

FROM alpine:3.22
RUN adduser -D -u 10001 birge
COPY --from=build /out/birge /out/birge-devlogin /out/birge-devsms /usr/local/bin/
USER birge
ENTRYPOINT ["birge"]
