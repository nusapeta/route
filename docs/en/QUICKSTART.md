# Quickstart

## From the release artifact

Requires Docker, curl, zstd, and sha256sum.

```bash
mkdir -p route-data && cd route-data
base=https://github.com/nusapeta/route/releases/download/data-2026.11.1

curl -fsSLO "$base/indonesia-v5-2026.11.graph.zst"
curl -fsSLO "$base/indonesia-v5-2026.11.graph.zst.sha256"
sha256sum -c indonesia-v5-2026.11.graph.zst.sha256
zstd -d indonesia-v5-2026.11.graph.zst

docker run --rm -p 127.0.0.1:8080:8080 -v "$PWD:/data:ro" \
  ghcr.io/nusapeta/route:0.2.0 \
  -graph /data/indonesia-v5-2026.11.graph -addr :8080
```

Check it:

```bash
curl -s 'http://127.0.0.1:8080/healthz' | jq '{ok, regions: [.regions[].name]}'
curl -s 'http://127.0.0.1:8080/route?from=-6.1754,106.8272&to=-6.5950,106.8166&format=polyline' \
  | jq '{distance_m, duration_s, points_n}'
```

The national graph is 552 MB compressed (1.6 GB decompressed). For a smaller
download, replace `indonesia-v5-2026.11` with `java-v5-2026.11`; that graph is
228 MB compressed, 693 MB decompressed, and covers Java only.

## Combined stack

Routing plus geocoding with checksum verification and a loopback-only gateway:

```bash
git clone https://github.com/nusapeta/stack.git
cd stack
cp .env.example .env
scripts/bootstrap.sh
docker compose up -d --wait
scripts/e2e.sh
```

See the [stack quickstart](https://github.com/nusapeta/stack/blob/main/docs/QUICKSTART.md).

## Build from a PBF

Requires Go 1.27.4+ and `make`.

```bash
git clone https://github.com/nusapeta/route.git
cd route
make build
make java
bin/nusa-serve -graph data/java.graph -addr 127.0.0.1:8080
```

For the national graph:

```bash
GOMEMLIMIT=5GiB scripts/build-national.sh
bin/nusa-serve -graph data/indonesia.graph -addr 127.0.0.1:8080
```

Expect about 5 GB of RAM during the national build. The extractor prints node
and edge counts; `/healthz` reports the same values from the finished file.

## Two profiles on one URL

```bash
bin/nusa-serve -graph data/indonesia.graph -profile car   -addr 127.0.0.1:18080
bin/nusa-serve -graph data/indonesia.graph -profile motor -addr 127.0.0.1:18081
bin/nusa-gateway -addr 127.0.0.1:8080
curl '127.0.0.1:8080/route?from=-6.1754,106.8272&to=-6.5950,106.8166&profile=motor'
```

The gateway has no authentication and no TLS. Keep it on loopback or behind a
proxy that overwrites `X-Forwarded-For`, and do not expose `/metrics`.

## Verify a checkout

```bash
make test
gofmt -l ./cmd ./internal
go vet ./...
go mod tidy -diff
scripts/smoke.sh http://127.0.0.1:8080
```

Full operational details: [`../OPERATIONS.md`](../OPERATIONS.md). API contract:
[`API.md`](API.md).
