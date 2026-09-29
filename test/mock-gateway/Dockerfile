# osiris-mock-gateway — TEST FIXTURE ONLY. See main.go's header for why it
# exists. Never referenced by docker-compose.yml; built and run only by
# bin/verify-agent-runtime.sh.
FROM golang:1.22.12-alpine3.20 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/mockgw .

FROM scratch
COPY --from=build /out/mockgw /mockgw
EXPOSE 8080
ENTRYPOINT ["/mockgw"]
