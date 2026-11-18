# HTTP API

Server: `nusa-serve` (one profile) or `nusa-gateway` (car + motorcycle on one
base URL). There is no authentication. Example base URL:
`http://127.0.0.1:8080`.

The Indonesian original is [`../API.md`](../API.md). If the two disagree, the
Indonesian file is canonical.

## Endpoint summary

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/route` | route from query parameters |
| `POST` | `/route` | route from a JSON body |
| `GET` | `/isochrone` | area reachable from one point |
| `GET` | `/healthz` | health + graph metadata |
| `GET` | `/version` | commit, build time, Go/OS/arch |
| `GET` | `/metrics` | Prometheus text format |
| `GET` | `/place/*` | geocoder bundle (gateway with `-place`) |
| `OPTIONS` | `*` | CORS preflight |

## `GET /place/*`

A gateway with `-place` strips the prefix and forwards to a separate geocoder,
so the upstream paths stay unchanged:

```http
GET /place/search?q=monas&limit=2
GET /place/reverse?lat=-6.175392&lon=106.827153
GET /place/lookup?osm_ids=N1308678148
GET /place/healthz
```

| Path | Upstream sees |
|---|---|
| `/place/search` | `/search` |
| `/place/reverse` | `/reverse` |
| `/place/lookup` | `/lookup` |
| `/place/healthz` | `/healthz` |

Bundle rules:

- `/place` alone returns `404`; the upstream has no index page.
- `/place/metrics` returns `404` when `-block-metrics` is active, same as
  `/metrics`; that endpoint exposes index size and age.
- A separate per-IP rate limit (default 240/min, burst 40) returns `429` with
  `Retry-After`. Geocoder traffic does not consume the routing quota.
- `X-Forwarded-For` and `X-Real-IP` are overwritten from `CF-Connecting-IP`, so
  clients cannot choose their own bucket.
- `/healthz` reports routing profiles only. A dead geocoder makes `/place/*`
  return `502` without marking the gateway unhealthy.

## `GET /route`

```http
GET /route?from=-6.1754,106.8272&to=-6.5950,106.8166&format=polyline
```

| Parameter | Required | Default | Values |
|---|---|---|---|
| `from` | yes | n/a | `lat,lng` or `{"lat":..,"lng":..}` |
| `to` | yes | n/a | same as `from` |
| `format` | no | `json` | `json`, `points`, `geojson`, `polyline` |
| `simplify` | no | `0` | meters; Douglas-Peucker, `0` = off |
| `restrictions` | no | `1` | `0` disables enforcement (debug) |
| `pin` | no | `0` | `1` adds raw pins at the ends |
| `profile` | no | `mobil` | gateway only: `mobil` or `motor` |

Coordinates must be inside Indonesia (lat −12…8, lon 93…142). Latitude comes
first, not longitude. Responses always echo the parsed `from`/`to` so a swapped
order is visible immediately.

### Response formats

Every format carries the base fields:

```json
{
  "from": [-6.1754, 106.8272],
  "to": [-6.5950, 106.8166],
  "distance_m": 56451,
  "duration_s": 2538.8,
  "region": "java",
  "snap_from_m": 4,
  "snap_to_m": 24,
  "snap_from": [-6.175401, 106.827203],
  "snap_to": [-6.594998, 106.816604],
  "bbox": [-6.5950, 106.8166, -6.1754, 106.8272],
  "points_n": 412
}
```

| `format` | Extra field |
|---|---|
| `json` / `points` | `points`: array of `[lat, lng]` |
| `geojson` | `geojson`: `FeatureCollection` with a `LineString` of `[lng, lat]` |
| `polyline` | `polyline` (Google encoded, precision 5) + `polyline_b64` |

`polyline` uses a charset that contains `\`. In JSON that becomes `\\`, so
copy-pasting from a JSON viewer can break a naive decoder. Applications should
use `polyline_b64` and base64-decode it.

Conditional fields:

| Field | Appears when |
|---|---|
| `ferry_m`, `has_ferry`, `ferry_spans` | the route uses a ferry |
| `legs` | cross-region route stitched with ferry connectors |
| `trimmed: {from,to}` | an initial/final spur was removed |
| `fallback: true` | restrictions were disabled to find a route |
| `relaxed: true`, `epsilon`, `turn_penalty` | the budget ran out and the heuristic was relaxed |

`ferry_spans` contains inclusive index ranges into the geometry array; use it to
draw dashed lines.

## `POST /route`

Maximum body size is 8 KiB. `from`/`to` may be strings or objects:

```bash
curl -s -X POST http://127.0.0.1:8080/route \
  -H 'Content-Type: application/json' \
  -d '{
    "from": {"lat": -6.1754, "lng": 106.8272},
    "to":   {"lat": -6.5950, "lng": 106.8166},
    "format": "geojson",
    "simplify": 5
  }'
```

Supported fields: `from`, `to`, `format`, `simplify`, `restrictions`, `pin`.
Other fields are ignored. Invalid JSON returns `400`.

## `GET /isochrone`

Area reachable from a single point within a travel-time budget. The response is
a GeoJSON `FeatureCollection`; each `Feature` is one closed polygon ring with
`[lng, lat]` coordinates.

```http
GET /isochrone?lat=-6.1754&lon=106.8272&minutes=15&meters=5000&grid=250&simplify=25
```

| Parameter | Required | Default | Values |
|---|---|---|---|
| `lat`, `lon` | yes | n/a | degrees; `lng` accepted as an alias of `lon` |
| `minutes` | no | `15` | `1`…`60` |
| `meters` | no | `100000` | `1`…`100000` |
| `grid` | no | `250` | `50`…`10000` metres; grows automatically past 40000 cells |
| `simplify` | no | `25` | metres; Douglas-Peucker, `0` = off |
| `profile` | no | `mobil` | gateway only: `mobil` or `motor` |

Rings come from a raster of cells visited by a cutoff-bounded Dijkstra search;
the effective cell size is reported in `meta.grid_m`. Rings have at least 8
vertices and 2000 m² of area; smaller ones are dropped.

```json
{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [[[106.827, -6.175], [106.828, -6.176], [106.827, -6.175]]]}}
  ],
  "meta": {"grid_m": 500, "nodes_visited": 3480, "area_m2": 16000000, "minutes": 15}
}
```

## Status and errors

| Status | `reason` | Meaning |
|---|---|---|
| `200` | n/a | route found |
| `304` | n/a | `If-None-Match` matched; no body |
| `400` | n/a | invalid parameter/JSON/coordinate |
| `422` | `snap` | no road within `-snap` (default 3 km) |
| `422` | `too_far` | straight-line or travel distance exceeds `-max-route-km` |
| `422` | `budget` | soft budget exhausted; route too long |
| `422` | `no_route` | graph is disconnected |
| `422` | `cross_region` | endpoints in different regions without a ferry connector |
| `429` | n/a | per-IP rate limit; includes `Retry-After` |
| `500` | n/a | internal error; details only in logs |
| `504` | `deadline` | hard `-timeout` deadline expired |
| `422` | `isochrone_too_large` | search exceeded the internal 1.5M node limit |
| `422` | `no_area` | no ring reached 2000 m² |

Error shape:

```json
{"error": "no road within snap distance (region not loaded?)", "reason": "snap"}
```

`reason` is stable client vocabulary. The `error` text may change. Every error
uses `Cache-Control: no-store` and carries no ETag.

## Cache and ETag

- `-maxage 3600` (default) enables `ETag` + `Cache-Control: public,
  max-age=3600` on `200`.
- `304` also carries an ETag; the body is empty.
- The validator is derived from the graph fingerprint and route parameters,
  not from wall-clock results. A changed graph produces a different ETag.
- `If-None-Match` is checked **before** rate limiting, so cache revalidation
  does not consume tokens.
- `429` never carries an ETag; errors must not be cached.
- `-maxage 0` disables ETag and successful-response `Cache-Control`.

## Rate limits and deadlines

Default: 15 requests/minute per IP, burst 5. The token bucket lives in each
process, not in shared storage. Behind a proxy, run with `-trust-proxy` and make
sure the proxy overwrites `X-Forwarded-For`/`CF-Connecting-IP`.

- `-timeout 10s`: hard deadline. A search that exceeds it returns `504 deadline`.
- `-budget`: soft budget, default 60% of the timeout. A search that exceeds it
  returns `422 budget`, deterministically and independent of load.
- `-max-expansions 4000000`: per-query expansion ceiling.
- `-max-route-km 100`: reject routes over 100 km; `0` disables.
- `-conc 4`: parallel queries; the rest queue.
- `-workspace-mb 256`: total A* tables, split evenly across `-conc`.

Rate limits are per process. The car and motorcycle containers have separate
buckets; the gateway adds no routing rate limit of its own.

## Response headers

| Header | Content |
|---|---|
| `X-Route-Ms` | routing wall time, milliseconds |
| `X-Route-Region` | graph file name that served the request |
| `X-Route-Expansions` | number of expanded edges |
| `X-Route-Trim` | `none`, `from`, `to`, or `both` |
| `X-Route-Profile` | `mobil`/`motor`, gateway only |
| `ETag`, `Cache-Control` | success/304 only |
| `Retry-After` | 429 only |
| `Access-Control-Expose-Headers` | the headers above for browsers |

## `/healthz`

Single-server response:

```json
{
  "ok": true,
  "version": "dev",
  "commit": "e1c4b0e",
  "regions": [
    {
      "name": "indonesia",
      "nodes": 54891393,
      "edges": 112340071,
      "min_lat": -11.02, "min_lon": 94.97,
      "max_lat": 6.08, "max_lon": 141.02,
      "bans": 3694,
      "only_rules": 154,
      "restrictions_skipped": 593,
      "format": 5,
      "pbf_sha256": "752489d3...",
      "built_at": "2026-11-12T11:12:59Z",
      "source": "indonesia-latest.osm.pbf",
      "extractor": "nusa-extract",
      "profile": "car+motor",
      "profile_sha256": "..."
    }
  ]
}
```

`ok` is `true` when at least one region is open. Provenance fields may be empty
for older graphs. The gateway wraps the response:

```json
{
  "ok": true,
  "profiles": {
    "mobil": {"ok": true, "status": 200, "upstream": { ... }},
    "motor": {"ok": true, "status": 200, "upstream": { ... }}
  }
}
```

The gateway considers itself healthy only when both upstreams are healthy. Do
not use `/healthz` as proof that a specific container is running; that is the
job of `docker ps`/systemd.

## `/version`

```json
{"commit":"e1c4b0e","built_at":"2026-11-12T...","go_version":"go1.27.4","goos":"linux","goarch":"amd64"}
```

`commit`/`built_at` are set through `-ldflags` during `make build`/`make static`.
Without them the values are `dev`/`unknown`.

## `/metrics`

Prometheus text format, no dependencies. Main metrics:

| Metric | Type | Purpose |
|---|---|---|
| `route_requests_total{code}` | counter | status 200/304/400/422/429/500/504 |
| `route_request_duration_seconds` | histogram | `/route` wall time |
| `route_inflight` | gauge | requests in flight |
| `route_expansions_total` | counter | total A* expansions |
| `route_fallback_total` | counter | routes without restrictions |
| `route_relaxed_total` | counter | routes with relaxed ε |
| `route_rate_limited_total` | counter | rejected by the limiter |
| `route_canceled_total` | counter | hard deadline |
| `route_errors_total{reason}` | counter | `snap`, `too_far`, `budget`, `no_route`, `cross_region` |
| `route_snap_retry_total` | counter | alternate snap after no-route |
| `route_cross_region_stitched_total` | counter | cross-region ferry routes |
| `nusa_route_workspace_slots` | gauge | total A* table slots |
| `nusa_route_workspace_bytes` | gauge | estimated 12 B/slot |
| `nusa_route_workspace_grows_total` | counter | growth rehashes |
| `nusa_route_workspace_shrinks_total` | counter | shrink rehashes |
| `nusa_route_build_info{commit,go_version}` | gauge | build identity |

Example query:

```bash
curl -s 127.0.0.1:18080/metrics | grep -E '^route_(requests|errors|expansions)|^nusa_route_workspace_'
```

## Flutter integration

Use `polylines` + `latlong2`. Decode `polyline_b64` to avoid JSON escaping:

```dart
import 'dart:convert';

List<List<double>> decodePolyline(String encoded) {
  var index = 0, lat = 0, lng = 0;
  final out = <List<double>>[];
  while (index < encoded.length) {
    var shift = 0, result = 0, b = 0;
    do {
      b = encoded.codeUnitAt(index++) - 63;
      result |= (b & 0x1f) << shift;
      shift += 5;
    } while (b >= 0x20);
    lat += (result & 1) != 0 ? ~(result >> 1) : (result >> 1);

    shift = 0;
    result = 0;
    do {
      b = encoded.codeUnitAt(index++) - 63;
      result |= (b & 0x1f) << shift;
      shift += 5;
    } while (b >= 0x20);
    lng += (result & 1) != 0 ? ~(result >> 1) : (result >> 1);

    out.add([lat / 1e5, lng / 1e5]);
  }
  return out;
}

final json = jsonDecode(response.body) as Map<String, dynamic>;
final raw = utf8.decode(base64Decode(json['polyline_b64'] as String));
final points = decodePolyline(raw)
    .map((p) => LatLng(p[0], p[1]))
    .toList();
```

For caching, store the `ETag` and send `If-None-Match`; `304` means the old
polyline is still valid. Never cache `422`/`429`/`504`.

## Tooling

- Bruno collection: `contrib/http/bruno/nusapeta-route/`.
- Postman: `contrib/http/nusapeta-route.postman_collection.json`.
- Smoke test: `scripts/smoke.sh http://127.0.0.1:8080`.

Bruno/Postman contain example coordinates and status assertions; update both
when the contract changes. Do not overwrite a user's modified Bruno files.
