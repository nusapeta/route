# Roadmap

**North star:** router jalan Indonesia yang paling ringan untuk rute ≤100 km —
RAM idle puluhan MB, artefak satu file, rebuild menit, dan hasil yang bisa
diaudit. Bukan engine latency tercepat; OSRM tetap lebih cepat.

Status: **2026-11-12**. Graph produksi `data/indonesia.graph`, v5, 54.891.393
node / 112.340.071 edge, 1.621 MB.

## Non-goals

Ditulis eksplisit supaya tidak menjadi backlog diam-diam:

- **Traffic / ETA real-time.** Butuh graph time-dependent dan data live; biaya
  memori serta reproduksibilitas memburuk.
- **Cakupan dunia.** Format dan profil dioptimalkan untuk Indonesia.
- **Turn-by-turn instruction.** Bukan routing graph; layanan hosted lebih tepat.
- **Multi-modal penuh.** Hanya mobil dan motor.
- **CH/MLD penuh.** Preprocessing dan artefak bertentangan dengan identitas
  ringan. Kalau rute >100 km dibutuhkan, dekomposisi dua tingkat lebih masuk
  akal daripada contraction penuh.
- **Akurasi ground truth tanpa data.** E2 tidak dijalankan; paper memakai
  limitation, bukan klaim akurasi.

## Budget

Setiap perubahan harus diukur terhadap tabel ini. Angka "sekarang" yang
berbeda mesin diberi label.

| Metrik | Sekarang | Target | Batas keras |
|---|---:|---:|---:|
| RSS idle nasional | **11,1 MB** (`anon` 4,5 MB) | ≤20 MB | 32 MB |
| Binary statis | 6,7 MB | ≤8 MB | 12 MB |
| Image container multi-stage | **36,7 MB** | ≤32 MB | 40 MB |
| Start → `/healthz` 200 | 10 ms | ≤25 ms | 100 ms |
| Ekstraksi region (Java, E1) | **28 s** | ≤90 s | 5 menit |
| Ekstraksi nasional | **1m12s** | ≤3 menit | 10 menit |
| Artefak region (Java) | **693 MB** | ≤800 MB | 1 GB |
| Artefak nasional | **1.621 MB** | dikecualikan | 2 GB |
| RSS 4× rute 96 km, `-conc 4` | 225 MiB (`anon` 90 MiB) | ≤1.536 MB cgroup | 1,5 GB |
| Throughput 42 km (i3) | **36–40 RPS** | ≥20 RPS | — |
| p95 server 42 km, `-conc 4` | 105–145 ms | ≤400 ms | 800 ms |
| p95 100 km (Ryzen) | 0,94–1,32 s | ≤1,5 s | 3 s |
| Galat snap p50 / p95 | **16,1 / 34,9 m** | ≤20 / ≤60 m | p95 150 m |
| Pelanggaran restriction | 0 | 0 | 0 |
| Fallback restriction | 0 | ≤1% | 1% |

Catatan: target image lama 25 MB **tidak tercapai**; image multi-stage saat ini
36,7 MB. Varian prebuilt belum diukur ulang setelah penambahan binary. Jangan
menulis "24 MB" tanpa pengukuran baru.

## Evaluasi (bukan budget)

E1 Java, 332 pasangan cocok:

| Metrik | Hasil | Gerbang | Status |
|---|---:|---:|---|
| Median \|Δ jarak\| | 0,23% | <0,1% | gagal |
| p95 \|Δ jarak\| | 14,49% | <12% | gagal |
| Max \|Δ jarak\| | 70,44% | — | dicatat |
| p95 durasi absolut | 10,22% | <15% | lulus |
| p50 latency Nusapeta Route | 38,7 ms | — | vs OSRM 1,5 ms |
| p95 latency Nusapeta Route | 674 ms | — | vs OSRM 3,7 ms |

D4 mengganti metrik primer ke p95 durasi absolut. Jarak tetap dilaporkan dan
gerbangnya tetap gagal. Tidak ada klaim akurasi; E1 hanya perbandingan engine.

## Milestone

| Milestone | Status | Bukti |
|---|---|---|
| M1 metadata + golden test | ✅ | provenance v3+, `/version`, 48 pasangan golden |
| M2 snap ke ruas | ✅ | p50 67,3 → 16,1 m; p95 114,0 → 34,9 m |
| M3 biaya per ekspansi | ✅ | 42 km 36–40 RPS; p50 93–102 ms |
| M4 produksi | ✅ | timeout, budget, rate limit, ETag, metrics; deploy tunnel |
| M5 cakupan | ✅ | graph nasional v5 54,9M/112,3M, 1,621 MB |
| M6 paper | 🟡 | draf lengkap; E1 selesai; E2 limitation; compile belum final |

### M1–M3 (selesai)

- Format v5: 320 B header, 14 section, node 12 B, edge 8 B, workspace 12 B/slot.
- Provenance PBF SHA-256, build time, extractor, profil, restriction.
- Golden test kota sintetis 30×30: 48 rute node + 48 rute snap.
- Snap ke ruas, trim/spur, `snap_from`/`snap_to`/`trimmed`.
- Weighted A* edge-state, turn penalty, workspace pool, Hilbert ordering.

### M4 (selesai)

- `-timeout` keras dan `-budget` lunak; `504 deadline` vs `422 budget`.
- Rate limit 15/menit burst 5, `Retry-After`.
- ETag dicek sebelum rate limit; `304` tidak memakai token.
- `/metrics` tanpa dependency.
- Bind loopback + Cloudflare Tunnel; `-trust-proxy`.
- Graceful shutdown 20 s.

### M5 (selesai)

- Satu artefak v5 untuk mobil + motor; dua proses, satu gateway.
- Tujuh region + graph nasional; ferry edge + connector antar-file.
- `-max-route-km 100` menjadi kontrak app.
- Format v4 memangkas Java 1.019 → 693 MB; v5 menambah motor tanpa duplikasi.
- Catatan: format v5 hanya bisa dibaca build saat ini; rollback butuh image dan
  graph yang cocok.

### M6 (paper, sedang berjalan)

- [x] E1 OSRM MLD di mesin sama; CSV final `paper/results/e1-osrm-aligned.csv`.
- [x] Audit restriction, turn penalty, kalibrasi tabel kecepatan.
- [x] E2 diputuskan tidak dijalankan (D1, 2026-11-10).
- [x] Draf `paper/main.tex` ditulis ulang dengan angka final.
- [x] Compile LaTeX dengan Tectonic 0.17.0: 6 halaman, tanpa overfull;
  bukan uji mesin bersih.
- [x] Review klaim di `README.md`, `paper/COMPARISON.md`, dan `main.tex`.
- [ ] Putuskan venue; jangan submit sebelum E2/limitation dan artifact
      reproducibility selesai.

## Pekerjaan berikutnya

Urutan berdasarkan risiko, bukan keinginan:

1. **Stabilkan graph nasional.** Pantau build ulang, ukuran, dan RSS setelah
   refresh data. Graph 1,6 GB adalah batas operasional, bukan target.
2. **Hierarchy dua tingkat** hanya jika ada kebutuhan rute >100 km. Ukur
   dulu berapa banyak permintaan yang ditolak `too_far`/`budget`; jangan
   menambah kompleksitas untuk kasus yang tidak diminta.
3. **Conditional restriction** (`restriction:conditional`) dan ferry schedule.
   Keduanya saat ini di-skip; dampaknya harus diukur sebelum diklaim.
4. **E2** hanya jika ada sumber ground truth yang bisa diaudit. Tanpa itu,
   limitation tetap.
5. **Image size.** Kembalikan ke ≤32 MB atau jelaskan komponen yang membesar.
   Jangan mengubah target tanpa pengukuran.

## Risiko

| Risiko | Dampak | Mitigasi |
|---|---|---|
| Graph nasional 1,6 GB | disk, page cache, build RAM | SSD, `mem_limit` 1,5 GB, arsip graph lama |
| Tanpa hierarchy | rute 80–100 km ekor 1–3 s | budget 6 s, tolak >100 km, ukur demand |
| Restriction di-skip | bisa melanggar larangan langka | `nusa-restcheck`, fallback metric, dokumentasi |
| E2 tidak ada | tidak bisa klaim akurasi | limitation eksplisit; fokus biaya per rute |
| Image membesar | identitas "ringan" melemah | ukur tiap build; prebuilt/static |
| Container tanpa auth | penyalahgunaan | loopback + proxy/TLS + rate limit |

## Cara mengukur

- Latency/throughput: `scripts/loadtest.py`, `bin/nusa-bench`.
- Memori: `/proc/<pid>/status` (`RssAnon` vs `RssFile`), bukan `ps rss`.
- Regresi rute: `go test ./internal/graph -run TestGoldenRoutes`.
- Restriction: `bin/nusa-restcheck`.
- Setiap angka paper harus punya perintah di `paper/RESULTS.md` dan status di
  `paper/COMPARISON.md`.
