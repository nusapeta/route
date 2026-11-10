# Paper draft: RouteID

Status: **draf lengkap, siap dikompilasi; E1 (baseline OSRM) selesai 2026-11-07,
E2 (ground truth) tidak dijalankan (D1 2026-11-10: limitation).**

| File | Isi |
|---|---|
| `main.tex` | Draf short paper (ACM `acmart`, `sigconf`, nonacm) |
| `refs.bib` | 19 referensi, DOI sudah diverifikasi lewat Crossref |
| `RESULTS.md` | Semua angka di paper + perintah untuk mereproduksinya |
| `EXPERIMENTS.md` | Checklist kekurangan (E1–E7) sebelum submit |
| `results/*.csv` | Data mentah benchmark (601 pasang, 4 konfigurasi) |

## Kompilasi

LaTeX belum terpasang di mesin ini (tidak ada `pdflatex`/`tectonic`). Dua opsi:

1. **Overleaf** (paling cepat): buat project baru, upload `main.tex` + `refs.bib`,
   set compiler pdfLaTeX, `acmart` sudah tersedia di TeX Live Overleaf.
2. Lokal:
   ```bash
   sudo pacman -S texlive-basic texlive-latexextra texlive-fonts-recommended
   pdflatex main && bibtex main && pdflatex main && pdflatex main
   ```

## Klaim yang sudah didukung data

Rencana kerja dan budget performa ada di
[`../docs/ROADMAP.md`](../docs/ROADMAP.md).

Ledger lengkap (klaim, angka, bukti, status) ada di
[`COMPARISON.md`](COMPARISON.md). Aturannya: hanya angka terukur yang boleh masuk
kolom routeid.

- Ekstraksi Java (tabel ekstraksi): 22,7 jt node, 46,7 jt edge, graf 693 MB
  (v3: 1 019 MB), 3m53s dengan 4 worker.
- Latency per bucket dengan 601 pasang tetap (tabel latency).
- ε=1.5 tidak mengubah panjang rute pada 526/526 pasang; ε=2.0 → 1 kasus +11 %
  (tabel inflation).
- Hilbert ordering: 7-20 % lebih cepat di p50, 7-16 % di p95, RSS metropolitan
  1.225 → 675 MB (tabel ordering).
- RSS per tahap beban (tabel RSS): 15 MB setelah start, 89 MB setelah 20 rute
  15 km, 385 MB setelah satu rute 148 km, 607 MB setelah satu rute 579 km.
- E1 vs OSRM MLD di mesin yang sama (tabel comparison): graf 693 MB vs 5,08 GB,
  preprocess 28 s vs 250 s, p50 35,1 ms vs 1,4 ms, p95 522 ms vs 3,4 ms.
- Jarak rute pada 332 pasang E1: median 0,23 %, p95 14,49 %, max 70,44 %;
  gerbang p95 <12 % dan median <0,1 % gagal. D4 mengganti metrik primer ke
  p95 durasi absolut 10,22 %. Sebab sisa selisih ada di `RESULTS.md` §18.

## Klaim yang **belum** didukung (jangan submit sebelum ini)

- Akurasi jarak/geometri vs referensi independen (Google/Mapbox). E2 tidak
  dijalankan (D1: limitation); E1 bukan pengganti karena OSRM memakai graf OSM
  yang sama.
- Pembanding selain OSRM MLD (Valhalla, GraphHopper, OSRM CH).
- Latency dan akurasi pada graf nasional (E5). Yang sudah terukur hanya build-nya:
  1.650 MB, 8m08s (`docs/ROADMAP.md` §M5).

Kalimat "reference points" di §5.7 sengaja dibuat eksplisit soal status ini.
