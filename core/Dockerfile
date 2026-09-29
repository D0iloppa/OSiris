# osiris-core — decision-making, Neo4j-writing service. NO claude binary, NO
# LLM gateway credentials anywhere in this image (A4/D12) — this is enforced
# by construction: the runtime stage starts from `scratch` (nothing but our
# static binary and CA certs), so there is no shell, no package manager, no
# way to `curl`/`apt install` an LLM CLI into it even by mistake later.
#
# Pinned to explicit version tags — no floating `latest` (same rule the
# [1단계] platform layer used: nginx:1.27-alpine, neo4j:5.24-community, etc.
# — see docs/design/IMPLEMENTATION-NOTES.md #1).

FROM golang:1.22.12-alpine3.20 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/osiris-core ./cmd/osiris-core

FROM alpine:3.20.3 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/osiris-core /osiris-core

USER 65532:65532
EXPOSE 8081 8090
HEALTHCHECK --interval=10s --timeout=3s --retries=3 --start-period=15s \
  CMD ["/osiris-core", "-healthcheck"]
ENTRYPOINT ["/osiris-core"]
