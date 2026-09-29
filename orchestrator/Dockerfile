# osiris-orchestrator — Docker Engine API driver + convergence loop. NO
# claude binary, NO LLM gateway credentials anywhere in this image (A4/D12,
# same enforcement-by-construction as core/Dockerfile): the runtime stage is
# `scratch`, so there is no shell, no package manager, nothing an LLM CLI or
# its credentials could hide behind later.
#
# Runs as root (UID 0) — unlike osiris-core, this is unavoidable: this
# process holds /var/run/docker.sock, which the design itself calls
# "사실상 호스트 root" (10-core-architecture.md §1.4). Dropping to a
# non-root UID here would not reduce this container's actual privilege (the
# socket IS root-on-the-host), it would only make the docker.sock group
# permission bit-matching fragile across hosts. The real boundary this image
# enforces is A4 (no LLM), not non-root.
#
# Pinned to explicit version tags — no floating `latest` (repo convention,
# IMPLEMENTATION-NOTES.md #1).

FROM golang:1.22.12-alpine3.20 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/osiris-orchestrator ./cmd/osiris-orchestrator

FROM alpine:3.20.3 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/osiris-orchestrator /osiris-orchestrator

EXPOSE 8081
HEALTHCHECK --interval=10s --timeout=3s --retries=3 --start-period=15s \
  CMD ["/osiris-orchestrator", "-healthcheck"]
ENTRYPOINT ["/osiris-orchestrator"]
