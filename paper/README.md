# Paper draft — RouteID

Status: **draf lengkap, siap dikompilasi; baseline & ground truth belum dijalankan.**

| File | Isi |
|---|---|
| `main.tex` | Draf short paper (ACM `acmart`, `sigconf`, nonacm) |
| `refs.bib` | 20 referensi, DOI sudah diverifikasi lewat Crossref |
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
kolom routeid; kolom pembanding menunggu E1.

- Ekstraksi Java: 58 s, peak 2.5 GB, graph 1 019 MB (Tabel 1).
- Latency per bucket dengan 601 pasang tetap (Tabel 2).
- ε=1.5 tidak mengubah panjang rute pada 526/526 pasang; ε=2.0 → 1 kasus +11 %
  (Tabel 3).
- Hilbert ordering: −7..20 % p50, −7..16 % p95, RSS metropolitan 1 225 → 675 MB
  (Tabel 4).
- RSS: 13 MB setelah start, 103 MB pada burst 50 km (Tabel 5).

## Klaim yang **belum** didukung (jangan submit sebelum ini)

- Perbandingan terkontrol dengan OSRM (E1).
- Akurasi jarak/geometri vs referensi (Google/Mapbox) (E2).
- Build nasional terukur — masih estimasi (E5).

Kalimat "reference points" di §5.7 sengaja dibuat eksplisit soal status ini.
