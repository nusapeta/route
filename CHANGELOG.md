# Changelog

All user-visible changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[SemVer](https://semver.org/). The `.graph` format has its own version (`v5`).

## [Unreleased]

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

[Unreleased]: https://github.com/nusapeta/route/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nusapeta/route/releases/tag/v0.1.0
