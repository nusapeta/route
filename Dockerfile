# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
# Provenance, same stamps as `make static`. Defaults keep the image
# reproducible; pass --build-arg COMMIT=$(git rev-parse --short HEAD) and
# --build-arg BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ) for a real build.
ARG COMMIT=dev
ARG BUILT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath \
      -ldflags="-s -w -X main.commit=${COMMIT} -X main.buildTime=${BUILT} -X github.com/nusapeta/route/internal/extract.Version=${COMMIT}" \
      -o /out/nusa-serve ./cmd/nusa-serve \
 && go build -trimpath -ldflags="-s -w" -o /out/nusa-gateway ./cmd/nusa-gateway

FROM alpine:3.24
RUN adduser -D -H -u 65532 nusapeta && apk add --no-cache ca-certificates
COPY --from=build /out/nusa-serve /usr/local/bin/nusa-serve
COPY --from=build /out/nusa-gateway /usr/local/bin/nusa-gateway
USER 65532:65532
EXPOSE 8080
# heap ceiling; lower it if the box is small, raise it if you serve very long routes
ENV GOMEMLIMIT=768MiB
# graph files are mounted at /data (never baked into the image)
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/nusa-serve"]
CMD ["-graph", "/data/java.graph", "-graph", "/data/sumatra.graph", "-addr", ":8080", "-conc", "2"]
