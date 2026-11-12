# Status eksperimen

Diperbarui **2026-11-12**. Dokumen ini hanya mencatat kesiapan bukti; angka
mentah ada di [`RESULTS.md`](RESULTS.md), ledger klaim di
[`COMPARISON.md`](COMPARISON.md).

| Eksperimen | Status | Bukti |
|---|---|---|
| E1 OSRM baseline | ✅ selesai | `RESULTS.md` §13 (historis), §18 (final) |
| E2 ground truth | ⛔ tidak dijalankan | D1: limitation eksplisit |
| E3 turn restriction | ✅ selesai | §7, §14; 0 pelanggaran / 0 fallback |
| E4 snap ke ruas | ✅ selesai | §9; p50 67,3 → 16,1 m |
| E5 build nasional | 🟡 build selesai, latency penuh belum | §19, `data/indonesia.graph` |
| E6 provenance + golden | ✅ selesai | format v5, golden 48 pasangan |
| E7 generalisasi | 🟡 sebagian | 7 pulau + graph nasional; belum ada uji kota independen |

## E1. Baseline OSRM

Ryzen 5 5600G, OSRM v5.26.0 MLD, PBF Java yang sama, 600 pasangan.

| Metrik | RouteID | OSRM |
|---|---:|---:|
| Cocok / gagal / dibuang | 332 / 79 / 189 | — |
| p50 latency, 332 cocok | 38,7 ms | **1,5 ms** |
| p95 latency | 674 ms | **3,7 ms** |
| Artefak | **693 MB** | 5,08 GB |
| Preprocess | **28 s** | 250 s |
| Jarak: median / p95 / max \|Δ\| | 0,23% / 14,49% / 70,44% | ref |
| Jarak >10% / >25% | 25 / 8 | — |
| Durasi: signed p50 / p95 | −1,11% / +2,39% | ref |
| Durasi absolut p95 | 10,22% | — |

79 kegagalan = 72 `snap` + 7 `budget`. OSRM tidak gagal, tetapi pada pasangan
itu snap target OSRM median 15,3 km dari input; bukan pembanding setara.

Gerbang jarak (median <0,1%, p95 <12%) **gagal**. D4 mengganti metrik primer
ke p95 durasi absolut; klaim akurasi tetap dicabut. CSV final:
`results/e1-osrm-aligned.csv`.

## E2. Ground truth

Tidak dijalankan. OSRM bukan ground truth karena memakai graf OSM yang sama.
Tidak ada API key dan tidak ada data referensi independen. Paper hanya boleh
menulis perbandingan antar-engine dan limitation.

Kalau nanti dijalankan: 200–500 pasangan, `distanceMeters` + polyline,
metrik median/p95 selisih jarak dan daftar kasus terburuk. Jangan memakai
Google Maps manual tanpa menyimpan screenshot + tanggal akses.

## E3. Turn restriction

Selesai pada graph final:

- 4.654 relasi dipindai, 3.962 usable, 3.808 ban + 154 `only_*`, 30,1 KB.
- 116 aturan dilonggarkan saat build (57 mencapai cap reachability), 593
  dibuang (via-way/conditional/unresolved).
- `rid-restcheck`: 3.463 sampel, **0 pelanggaran, 0 fallback**.
- Graph v5 menyimpan ban mobil 3.694 + 154 `only_*`; motor 3.589 + 136.

Sisa: `restriction:conditional` dan via-way yang masih konservatif. Keduanya
batas model, bukan bug yang disembunyikan.

## E4. Snap ke ruas

Selesai. Proyeksi ke segmen + trim/spur, tanpa pseudo-node.

| Metrik | Node | Ruas |
|---|---:|---:|
| Galat p50, segmen >150 m | 67,3 m | **16,1 m** |
| Galat p95 | 114,0 m | **34,9 m** |

Medan–Binjai: offset 45 → 4 m, jarak rute −58 m. Sisa kasus: spur keluar arah
lain masih garis lurus sepanjang `Along`; bukan klaim akurasi rute.

## E5. Build nasional

Selesai untuk build dan memori:

- `data/indonesia.graph`: 54.891.393 node / 112.340.071 edge,
  1.621.222.584 B, v5 `car+motor`.
- Build 11m40s termasuk unduh; ekstraksi 1m12s, `GOMEMLIMIT=5GiB`.
- Idle 11,1 MB (`anon` 4,5 MB); 4× rute 96 km 225 MiB; 1× 1.882 km 701 MiB.

**Belum:** latency satu graph nasional penuh. Angka 100 km (481/600, p95
0,94–1,32 s) diukur dengan tiga region Java di Ryzen, bukan graph nasional
tunggal. Jangan menulis "national latency" untuk angka itu.

## E6. Provenance dan reproduksibilitas

Selesai:

- Format v5: header 320 B + 14 section, SHA-256 PBF, build time, profil,
  restriction, `motor_sha256`.
- Golden test 48 pasangan sintetis (node + snap), round-trip provenance,
  kompatibilitas versi lama.
- `/version`, `/healthz`, CSV mentah di `results/`.
- Semua tabel punya perintah reproduksi di `RESULTS.md`.

Sisa opsional: ulangi benchmark 5× dan laporkan median antar-run; pin CPU.
E1 sudah dijalankan bergantian, tetapi belum 5×.

## E7. Generalisasi

Sebagian: graph nasional mencakup tujuh pulau dan ferry; tujuh koordinat lokal
diuji 200. Belum ada evaluasi kota independen (Bandung/Surabaya) dengan
pasangan tetap, dan belum ada OSRM/Valhalla pada graph nasional.

## Sebelum submit

- [ ] Compile `main.tex` di mesin bersih; tidak ada overfull yang parah.
- [ ] Semua klaim di abstrak ada di `RESULTS.md` dan `COMPARISON.md`.
- [ ] Tidak ada kata "akurat"/"tercepat" tanpa kualifikasi.
- [ ] E2 tetap limitation atau dijalankan dengan data yang bisa diaudit.
- [ ] Full-national latency diukur atau tidak diklaim.
