# Changelog

All user-visible changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[SemVer](https://semver.org/). The `.graph` format has its own version (`v5`).

## [Unreleased]

## [0.2.1] - 2026-11-20

### Fixed

- `/route` no longer answers 422 for a short trip whose nearest segment snaps
  into a dead pocket while a usable segment sits tens of metres away. The snap
  retry now probes its candidate pairs across the whole candidate span instead
  of taking the nearest few, because a dead cluster wider than the retry can be
  interleaved with live segments by distance. Measured on the Jakarta extract,
  `-6.1754,106.8272 -> -6.20,106.80` with `avoid=toll` now answers 200 with a
  41 m snap instead of 422 after exhausting the budget (22 of the 32 goal
  candidates were dead, the first live one at 32 m).
- A retry search is now capped by the distance between its own two snaps
  instead of the whole request budget, so a dead candidate fails in well
  under a second rather than each walking a whole component. `RouteOptions`
  gained `Expansions` for this one-query override, honoured by both the A*
  router and the one-to-many Dijkstra.

## [0.2.0] - 2026-11-20

Sprint 18–19 November 2026. Binary and data release: the graph must be rebuilt
because the motorcycle profile changed (`motor_sha256`); routing behaviour with
the old graph is unchanged.

### Added

- `GET /matrix`: one-to-many travel matrix, up to 5 sources × 25 targets
  (25 pairs maximum), one Dijkstra per source, `max_minutes` 1–180. Cells are
  `ok`, `too_far`, `snap_failed` or `no_route`; straight-line pairs above
  100 km never reach the search.
- `GET /isochrone`: reachable area as a GeoJSON `FeatureCollection` built from
  a bounded Dijkstra raster, with `minutes`, `meters`, `grid`, `simplify` and
  gateway-only `profile`. Rings below 8 vertices or 2000 m² are dropped.
- Static ferry table for the ten main crossings (Merak–Bakauheni,
  Ketapang–Gilimanuk, Padang Bai–Lembar, Lembar–Poto Tano, Poto Tano–Sape,
  Sape–Bima, Ujung–Kamal, Batam–Tanjung Pinang, Sibolga–Teluk Dalam,
  Makassar–Parepare). OSM `duration` still wins at extraction; the table fills
  gaps and annotates responses. `ferry_time_source` is `osm` or `schedule`;
  no live timetable is queried.
- One-source many-target Dijkstra (`DijkstraTree`) behind `/matrix`, so a
  source's targets are read from a single search.
- CI budget guard on RSS, artifact size, image size and binary size.
- `/route` accepts `avoid=toll|ferry|unpaved|motorway`. The mask is applied
  inside A* edge expansion, so it changes the path and the recorded expansion
  count instead of filtering a finished route. Classes are inferred from the
  graph's speed class because the artefact stores no per-edge OSM road type.
- `/route` accepts `alt=1..3` for up to three edge-banned searches. Returned
  `alternatives` differ from the primary route by at least 20% of edge length.
- `/route` accepts `via=lat,lng|...` (max 5 points) and returns `legs[]` with
  per-leg distance, duration and geometry indexes.
- `GET /nearest?lat=&lon=&k=&max=` wraps `SnapEdges` without any graph search,
  for pin previews and snapping diagnostics.

### Changed

- The map demo left this repository. `nusa-gateway` now answers JSON only;
  the page, styles, and assets live in
  [`nusapeta/demo`](https://github.com/nusapeta/demo), which serves them from
  nginx and proxies `/route` and `/place` back here. `GET /` and `/assets/*`
  on a gateway-only deployment answer `404`.
- Compose: the gateway service is now named `api` (container names unchanged).
  Compose adds a service's own name as a network alias, so a service called
  `gateway` would keep the tunnel name `gateway` on the API container and split
  the ingress with the demo container that owns it. Update any
  `docker compose up -d gateway` habit to `api`; the homelab override and
  `deploy/README.md` follow the new name.

- Motorcycle `surface` speed factors now cover 24 OSM values across the same
  four quality tiers (added chipseal, concrete plates, sett, cobblestone,
  bricks, grass_paver, pebblestone, stone, rock, grass, and others). The car
  profile, graph format, and 5 km/h floor are unchanged; no new runtime
  dependency or artifact bytes. Rebuild graphs so stored `MCls` matches the
  new `motor_sha256`.

### Documentation

- `restriction:conditional` is now an explicit unsupported case with a written
  reason: the static v5 graph has no departure time or timezone model, no
  verified time-window fixture exists, and v6 windows need an ENG budget update
  first. Relations stay counted as skipped and never enter the graph, so
  fallback, golden routes, and artifact size are unchanged. Extract logs now
  emit `conditional_note=` when such relations are present.

### Fixed

- Container image: `nusa-gateway` was built without the version stamp, so the
  map served its assets with a constant `?v=dev` and an upgrade could leave an
  old script in the browser cache. The Dockerfile now passes `COMMIT` to the
  gateway build too. The published `0.1.0` image predates this fix.

## [0.1.0] - 2026-11-15

First public release. Project name: Nusapeta Route (previously routeid).

### Added

- `nusa-extract`: Indonesian PBF into one mmap graph with car and motorcycle
  profiles, format v5.
- `nusa-serve`: `/route`, `/healthz`, `/version`, `/metrics`; two profiles from
  one graph.
- `nusa-gateway`: one base URL with `?profile=mobil|motor`, plus an optional
  `/place` prefix to the Nusapeta Place geocoder.
- `nusa-bench`, `nusa-restcheck`, `nusa-graphinfo`, `nusa-tagstats`,
  `nusa-snapcheck`, `nusa-restats`, `nusa-route` for operations and data audit.
- Demo map at `/` and a Bruno/Postman request collection in `contrib/http/`.
- `data-2026.11` data artifact (national graph) under ODbL 1.0.

### Known limitations

- Indonesia only; long inter-island routes need a two-level graph.
- 100 km straight-line route cap; above that the server returns 422.
- ETA accuracy: E1 gate median difference 0.23%, p95 14.49%; this is not a claim
  of parity with OSRM.
- No built-in authentication; per-IP rate limit only.

[Unreleased]: https://github.com/nusapeta/route/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/nusapeta/route/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/nusapeta/route/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/nusapeta/route/releases/tag/v0.1.0
