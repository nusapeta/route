# Security

## Reporting a problem

Do not open a public issue for an exploitable vulnerability. Use the GitHub
private security advisory at
<https://github.com/nusapeta/route/security/advisories/new>. Include:

- the commit version (`/version`) or image tag;
- reproduction steps;
- possible impact;
- whether the problem is reachable over HTTP or only through a corrupt graph.

An acknowledgement is sent within 7 days, best-effort. There is no SLA and no
promise of a fix date. Fixes are recorded in `CHANGELOG.md` after release.

## Threat model

| Asset | Threat | Mitigation |
|---|---|---|
| Server process | Hostile HTTP input | Length, range, and type validation; `-max-route-km`, `-max-expansions`, `-timeout` |
| Graph | Corrupt or modified file | `RIDG` magic, format version, mmap offset bounds |
| PBF parser | Hostile file | `paulmach/osm`; the graph is built offline on the host |
| Dependencies | Vulnerable module | `govulncheck` in CI, Dependabot |
| Container | Write access or root | non-root UID 65532, read-only graph, no shell in the image |
| Data | OSM data misuse | ODbL attribution in `/healthz` and the demo page |

## Boundaries

Nusapeta Route does not handle authentication, TLS, or accounts. There is a
per-IP rate limit, but it is not a replacement for a reverse proxy when you
need access control. Do not expose `/metrics` to the internet; the gateway
blocks it with `-block-metrics`.

## What counts as a vulnerability

- A crash or panic from a valid HTTP request.
- An out-of-bounds read from a graph opened with verification.
- Code execution from PBF or graph input.
- Filesystem path or stack trace leakage in a response.
- Denial of service that existing limits cannot bound.
- A rate-limit bypass through `X-Forwarded-For` when `-trust-proxy` is enabled
  without a trusted proxy in front.

## What does not count

- Wrong routes caused by incomplete OSM data or incorrect access tags.
- Different results from OSRM or commercial navigation.
- Routes above 100 km answered with 422; that is a product limit.
- Latency on hardware below the documented specification.

## Dependencies

- `go.mod` and `go.sum` are committed; CI runs `go mod tidy -diff`.
- `govulncheck` runs on every push and pull request.
- New dependencies MUST be explained in the pull request.
- Graphs and PBFs never enter git.
