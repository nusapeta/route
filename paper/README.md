# Paper draft: RouteID

Status: **draf lengkap; E1 final 2026-11-10; E2 tidak dijalankan (D1:
limitation).** Dikompilasi 2026-11-12 dengan Tectonic 0.17.0 (6 halaman);
bukan uji mesin bersih dan belum submit.

| File | Isi |
|---|---|
| `main.tex` | Draf ACM `acmart` (`sigconf`, `nonacm`) |
| `refs.bib` | 19 referensi; jangan menambah entri tanpa verifikasi DOI |
| `RESULTS.md` | Angka mentah + provenance; snapshot kanonik di atas |
| `COMPARISON.md` | Ledger klaim yang boleh dan tidak boleh dipakai |
| `EXPERIMENTS.md` | Status E1–E7 dan checklist submit |
| `results/*.csv` | CSV mentah; final E1: `e1-osrm-aligned.csv` |

## Kompilasi

Tidak ada `pdflatex`/`tectonic` di mesin pengembangan. Opsi:

1. **Overleaf**: upload `main.tex` + `refs.bib`, compiler pdfLaTeX; `acmart`
   sudah tersedia.
2. **Tectonic static** (tanpa instal TeX Live):
   ```bash
   curl -fsSL https://drop-sh.fullyjustified.net | sh
   ./tectonic --outdir /tmp/routeid-paper paper/main.tex
   ```
   Jangan commit PDF/aux kecuali diminta.

Kompilasi terakhir memakai Tectonic static dan bundle cache lokal:
`tectonic -C --keep-logs --outdir /tmp/routeid-paper paper/main.tex`.

## Angka final yang dipakai

Mesin Ryzen 5 5600G, Java, 600 pasangan; 332 cocok setelah filter snap.

| Metrik | RouteID | OSRM MLD |
|---|---:|---:|
| Artefak | **693 MB** (1 file) | 5,08 GB |
| Preprocess | **28 s** | 250 s |
| RSS sebelum query | 17 MB | 2,36 GiB |
| p50 latency, 332 cocok | 38,7 ms | **1,5 ms** |
| p95 latency | 674 ms | **3,7 ms** |
| Jarak: median / p95 / max \|Δ\| | 0,23% / 14,49% / 70,44% | ref |
| Jarak >10% / >25% | 25 / 8 | — |
| Durasi: signed p50 / p95 | −1,11% / +2,39% | ref |
| Durasi absolut p95 | 10,22% | — |

79 gagal (72 `snap`, 7 `budget`), 189 dibuang filter snap. Pada 79 kegagalan
itu snap target OSRM median 15,3 km; OSRM tidak gagal, tetapi bukan pembanding
setara.

Graph nasional v5: 54.891.393 node / 112.340.071 edge, 1.621 MB; build 11m40s
termasuk unduh, ekstraksi 1m12s. Batas 100 km: 481/600 sukses, p95 0,94–1,32 s
di tiga region Ryzen. **Belum** ada latency satu graph nasional penuh.

## Klaim yang didukung

- Artefak dan preprocess lebih kecil/cepat dari OSRM MLD pada Java.
- RSS idle dan setelah beban lebih kecil dari OSRM.
- RouteID lebih lambat; jangan menulis "tercepat".
- Jarak relatif terhadap OSRM: median 0,23%, p95 14,49%; gerbang p95 <12%
  gagal. D4 memakai p95 durasi absolut 10,22% sebagai metrik primer.
- ε=1,5 mempertahankan panjang rute 526/526; Hilbert 7–20% p50 lebih cepat.
- Restriction: 0 pelanggaran/0 fallback; snap ruas p50 16,1 m.

## Klaim yang belum didukung

- **Akurasi** vs referensi independen. E2 tidak dijalankan; OSRM bukan ground
  truth.
- Pembanding Valhalla/GraphHopper/OSRM CH.
- Latency/akurasi pada satu graph nasional penuh.
- Dampak `restriction:conditional` dan jadwal ferry.
- Klaim "siap produksi" tanpa kualifikasi; repo privat, satu deployment.

## Struktur

1. Introduction
2. Workload and requirements
3. Design (extraction, binary v5, Hilbert, weighted A*, snapping, profiles)
4. Evaluation setup
5. Results (artifact, E1, heuristic, ordering, memory, restrictions, snapping,
   OSRM comparison, national/100 km, throughput)
6. Discussion
7. Threats to validity
8. Related work
9. Conclusion and future work

Paragraf **Artifact and reproducibility** ada di §6 dan menunjuk `RESULTS.md`,
`COMPARISON.md`, dan skrip di `scripts/`; tidak mengklaim artefak sudah public.
