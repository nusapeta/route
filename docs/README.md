# Dokumentasi RouteID

Status snapshot: **2026-11-12**. Graph produksi: `data/indonesia.graph`, format
v5, 54.891.393 node / 112.340.071 edge, 1.621 MB.

## Mulai dari mana

| Kebutuhan | Dokumen |
|---|---|
| Mencoba RouteID | [`../README.md`](../README.md) |
| Memahami format dan algoritma | [`ARCHITECTURE.md`](ARCHITECTURE.md) |
| Mengintegrasikan app | [`API.md`](API.md) |
| Build, refresh graph, deploy | [`OPERATIONS.md`](OPERATIONS.md) |
| Memeriksa angka performa | [`BENCHMARKS.md`](BENCHMARKS.md) |
| Merencanakan pekerjaan | [`ROADMAP.md`](ROADMAP.md), [`BACKLOG.md`](BACKLOG.md) |
| Model motor | [`MOTOR-MODEL.md`](MOTOR-MODEL.md) |
| Paper dan bukti | [`../paper/README.md`](../paper/README.md) |

## Sumber kebenaran

Urutan prioritas saat dokumen bertentangan:

1. **Kode dan test** di `internal/`, `cmd/`, dan `web/`.
2. **Metadata graph** dari `bin/rid-graphinfo -graph data/indonesia.graph` dan
   respons `/healthz`.
3. **Angka mentah** di [`../paper/RESULTS.md`](../paper/RESULTS.md), lengkap
   dengan mesin, data, tanggal, dan perintah reproduksi.
4. **Ledger klaim** di [`../paper/COMPARISON.md`](../paper/COMPARISON.md).
5. Dokumen naratif di folder ini dan `README.md`.

Dokumen tidak boleh memuat angka performa tanpa menyebut mesin, graph, dan
bagian `RESULTS.md` yang menjadi bukti.

## Peta dokumen

### Dokumen aktif

| File | Status | Isi |
|---|---|---|
| [`../README.md`](../README.md) | aktif | pitch, quick start, snapshot, batasan |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | aktif | pipeline, format v5, A*, snap, restriction, profil |
| [`API.md`](API.md) | aktif | kontrak HTTP, error, cache, Flutter, tooling |
| [`OPERATIONS.md`](OPERATIONS.md) | aktif | build, data, deploy, monitoring, kapasitas |
| [`BENCHMARKS.md`](BENCHMARKS.md) | aktif | metodologi + angka kanonik |
| [`ROADMAP.md`](ROADMAP.md) | aktif | north star, budget, milestone, non-goals |
| [`BACKLOG.md`](BACKLOG.md) | aktif | pekerjaan yang belum selesai, prioritas, alasan ditunda |
| [`MOTOR-MODEL.md`](MOTOR-MODEL.md) | aktif | apa yang dimodelkan dan tidak dimodelkan untuk motor |
| [`../deploy/README.md`](../deploy/README.md) | aktif | langkah deploy host, Compose, systemd, tunnel |
| [`../paper/README.md`](../paper/README.md) | aktif | status paper, compile, peta bukti |
| [`../paper/RESULTS.md`](../paper/RESULTS.md) | aktif | catatan pengukuran mentah |
| [`../paper/COMPARISON.md`](../paper/COMPARISON.md) | aktif | klaim yang boleh dan tidak boleh ditulis |
| [`../paper/EXPERIMENTS.md`](../paper/EXPERIMENTS.md) | aktif | eksperimen yang masih kurang untuk submission |

### Dokumen historis

Dokumen berikut menyimpan keputusan dan rencana pada tanggal tertentu. Jangan
memakai statusnya sebagai kondisi sekarang; gunakan `ROADMAP.md` dan
`RESULTS.md`.

| File | Tanggal | Catatan |
|---|---|---|
| [`M6.md`](M6.md) | 2026-11-07 s/d 2026-11-10 | resep E1 OSRM; E2 dilewati |
| [`PLAN-2026-11-10.md`](PLAN-2026-11-10.md) | 2026-11-10 | rencana motor, akurasi, cakupan |
| [`PLAN-resource-accuracy.md`](PLAN-resource-accuracy.md) | 2026-11-08 | rencana resource vs akurasi |

Folder `paper/results/` berisi CSV mentah; `docs/bruno/` berisi koleksi request
Bruno. Keduanya bukan dokumentasi naratif.

## Aturan menulis

- **Pisahkan mesin.** Angka i5-7360U, Ryzen 5 5600G, dan i3-6100T tidak boleh
  dicampur dalam satu tabel tanpa label.
- **Sebut graph.** Format v4 dan v5 tidak selalu sebanding; tulis versi, jumlah
  node/edge, dan apakah artefak mobil, motor, atau union.
- **Sebut tanggal.** Data OSM berubah; angka lama tetap sah untuk graph yang
  diukur, bukan untuk graph hari ini.
- **Jangan klaim akurasi.** E2 tidak dijalankan. E1 hanya perbandingan
  antar-engine.
- **Perbarui tiga tempat sekaligus** saat kontrak HTTP berubah: `README.md`
  ringkas, `docs/API.md`, dan assertion Bruno/Postman.
- **Hapus, jangan biarkan.** Kalau item backlog selesai, pindahkan hasilnya ke
  `RESULTS.md`/`ROADMAP.md` dan hapus dari `BACKLOG.md`.
