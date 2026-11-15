# Changelog

Semua perubahan yang terlihat pengguna dicatat di sini. Format mengikuti
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versi mengikuti
[SemVer](https://semver.org/). Format `.graph` punya versi sendiri (`v5`).

## [Unreleased]

## [0.1.0] - belum dirilis

Rilis publik pertama. Nama proyek: Nusapeta Route (sebelumnya routeid).

### Added

- `nusa-extract`: PBF Indonesia menjadi satu graph mmap berisi profil mobil dan
  motor, format v5.
- `nusa-serve`: `/route`, `/healthz`, `/version`, `/metrics`; dua profil dari
  satu graph.
- `nusa-gateway`: satu base URL dengan `?profile=mobil|motor`, plus prefix
  `/place` opsional ke geocoder Nusapeta Place.
- `nusa-bench`, `nusa-restcheck`, `nusa-graphinfo`, `nusa-tagstats`,
  `nusa-snapcheck`, `nusa-restats`, `nusa-route` untuk operasi dan audit data.
- Demo peta di `/` dan kumpulan request Bruno/Postman di `contrib/http/`.
- Artefak data `data-2026.11` (graph nasional) dengan lisensi ODbL 1.0.

### Known limitations

- Cakupan Indonesia saja; rute antarpulau panjang belum dilayani tanpa graph
  dua tingkat.
- Batas rute 100 km garis lurus; di atas itu dijawab 422.
- Akurasi ETA: gate E1 median selisih 0,23%, p95 14,49%; bukan klaim setara
  OSRM.
- Tanpa autentikasi bawaan; rate limit per IP.

[Unreleased]: https://github.com/nusapeta/route/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/nusapeta/route/releases/tag/v0.1.0
