# Roadmap routeid

**North star:** alternatif OSRM / Google Routes yang paling ringan —
router jalan Indonesia yang memakai puluhan MB, bukan gigabyte, dan tetap
menjawab rute <=50 km di bawah setengah detik di perangkat murah.

## Non-goals (sengaja ditolak)

Menolak hal-hal ini adalah bagian dari strategi, bukan kekurangan sementara:

- **Traffic / ETA real-time.** Butuh data live + penyimpanan time-dependent
  graph; membunuh budget memori dan membuat hasil tak dapat direproduksi.
- **Cakupan dunia.** Fokus Indonesia; format graph boleh mengasumsikan tabel
  kecepatan per kelas jalan lokal.
- **Turn-by-turn instruction.** Itu pekerjaan NLP atas nama jalan; layanan
  hosted lebih tepat. Kalau nanti dibutuhkan, ditambahkan sebagai modul terpisah
  yang membaca artefak yang sama.
- **Multi-modality penuh** (sepeda, angkutan umum). Motor masuk karena
  relevansi pasar Indonesia; sisanya tidak.
- **CH/MLD penuh.** Preprocessing puluhan menit dan artefak berlipat bertentangan
  dengan identitas "ringan". Kalau rute panjang dibutuhkan, pakai dekomposisi
  dua tingkat, bukan contraction penuh.

## Budget "paling ringan" (angka yang mengikat)

Setiap perubahan harus lulus tabel ini. Jika sebuah fitur melewati budget, ia
butuh argumen eksplisit dan milestone sendiri.

| metrik | sekarang (terukur) | target M3 | batas keras |
|---|---|---|---|
| RSS idle (2 region termuat) | 15 MB | <= 20 MB | 32 MB |
| Binary statis | 6,6 MB | <= 8 MB | 12 MB |
| Image container | 23 MB | <= 25 MB | 40 MB |
| Start sampai `/healthz` 200 | 10 ms | <= 25 ms | 100 ms |
| Preprocess per pulau | 58 s | <= 90 s | 5 menit |
| RSS @ beban 42 km, `-conc 4` | 558 MB | <= 350 MB | 700 MB |
| Throughput 42 km (i3-6100T) | 10,9 RPS | >= 20 RPS | - |
| Densitas | 19,5 RPS/GB | >= 45 RPS/GB | - |
| p95 klien 42 km, `-conc 4` | 439 ms | <= 400 ms | 800 ms |
| Galat ujung rute (snap) | **p50 16 m, p95 36 m** (offset uji <=40 m) | p50 <= 20 m, p95 <= 60 m | p95 150 m |
| Pelanggaran turn restriction | 0 (3,17% fallback) | 0 (fallback <= 1%) | 0 |

"Densitas" = RPS / RSS, metrik utama proyek ini: satu GB RAM harus menampung
sebanyak mungkin rute per detik.

## Milestone

### M1 — Bisa dipercaya — SELESAI

Tujuan: artefak bisa diaudit dan regresi ketahuan otomatis. Tanpa ini, angka
performa tidak bisa dibandingkan antar build.

- [x] Metadata graph format **v3**: SHA-256 pbf + waktu build di header, blob
      `key=value` di akhir file (`source`, `pbf_sha256`, `pbf_bytes`,
      `extractor`, `profile_sha256`, `order`, `grid_step`, `restrictions`).
      Terbaca di `/healthz`, `/version`, dan `rid-graphinfo`. v1/v2 tetap dimuat.
- [x] Build info di binary via `-ldflags` (`main.commit`, `main.buildTime`,
      `extract.Version`), otomatis dari `make build` / `make static`.
- [x] Golden test 48 pasangan pada kota sintetis 30x30, plus round-trip
      provenance dan uji kompatibilitas v2.
- [x] Biaya diukur: hash pass menambah ~0,3 s dari 21,2 s total ekstraksi
      Sumatra (peak 1.661 MB, sama seperti sebelumnya); blob metadata 288 byte.

**Terbukti:** `format: v3`, `pbf_sha256: dcbc563f…` di `/healthz`; mengubah satu
kecepatan di test menggagalkan 3 pasangan (harness tidak vakum).

### M2 — Akurasi ujung rute — SELESAI

- [x] `SnapEdge` (proyeksi ke ruas) + `RouteSnap` (trim bila rute keluar lewat
      ruas itu, spur bila tidak). Tanpa perubahan format, tanpa pseudo-node.
- [x] `/route` dan `rid-route` memakai jalur ruas; respons memuat `snap_from`,
      `snap_to`, `trimmed`, header `X-Route-Trim`; `&pin=1` = perilaku lama.
- [x] `rid-snapcheck`: node vs ruas vs oracle, bucket panjang ruas, opsi `-bbox`.
- [x] Angka di `RESULTS.md` §9, golden test memuat 48 pasangan snap.

**Hasil:** ruas >150 m: 67,3 → 16,1 m p50 (p95 114,0 → 34,9 m). Offset pin Medan
45 → 4 m; jarak rute terkoreksi 58 m; geometri mulai/berakhir di jalan.
**Sisa:** kasus spur (rute keluar arah lain) masih garis lurus sepanjang `Along`.

### M3 — Naikkan densitas 2x (3-5 hari)

Tujuan: satu box kecil menampung ~1.200 user (1 rute/menit), bukan 600.

- [x] Heuristik tanpa trigonometri: satu lookup kosinus + satu `sqrt` per
      relaksasi (sebelumnya 3 panggilan `math.Sin`/`Cos`/`Atan2`). Rata-rata
      39% CPU sebelumnya habis di `Haversine`.
- [x] Tabel workspace dipadatkan: kunci + generasi berbagi satu `uint32`
      (`gen<<nodeBits | edge+1`), jadi 12 B/slot (dari 16 B/slot, −25%) dan satu
      array lebih sedikit untuk di-probe.
- [x] Batas turn restriction diambil sekali per ekspansi (`Graph.Span`) lalu
      dicek linear, bukan dua `sort.Search` per tetangga.
- [x] Heap frontier 4-ary (pohon lebih dangkal) dan load factor tabel 0,8.

**Hasil terukur (Java, 601 pasang, ε=1,5, box uji 2 core):** p50 bucket 30–50 km
406 → 206 ms dan 523 → 254 ms (rasio per-pasangan median 1,9x, 2,0x); throughput
`-conc 2` pada satu rute 41,7 km 7,2 → 12,1 RPS. **Jumlah ekspansi sama**
(815k vs 818k) — kemenangannya murni biaya per ekspansi. Waktu tempuh 537/537
pasang identik; 1 pasang memilih geometri lain dengan waktu sama.

**Tidak dikerjakan (dengan alasan terukur):**

- ALT ditunda: `d(l,·)` untuk 4 landmark sebagai `uint16` tetap butuh
  2 x 4 x 2 B x 22,7 jt node = 182 MB untuk Java, dan bidirectional butuh
  reverse CSR ~278 MB — keduanya melewati batas 350 MB sebelum workspace.
  Lihat BACKLOG item 6 untuk skema sidecar `map.alt` (opt-in, di luar budget).
- Alokasi awal workspace dari perkiraan jarak: grow yang ada sudah jarang
  (tabel berhenti tumbuh setelah query panjang pertama) dan `-conc` membatasi
  puncaknya, jadi tidak ada angka yang bisa diperbaiki.

**Selesai bila:** >= 20 RPS @42 km di i3, RSS beban <= 350 MB, dan jarak rute
identik dengan baseline (dibuktikan golden test M1).

**Status: tercapai dan sudah ter-deploy** (`45de2d0`, `routeid:local` di homelab,
image lama disimpan sebagai `routeid:pre-m3`). Diukur bergantian di box yang
sama, rute 41,7 km, `-conc 4`:

| metrik | target M3 | pre-m3 | sesudah |
|---|---|---|---|
| RPS 42 km (i3-6100T) | >= 20 | 15,8-21,7 | **36,1-40,1** |
| server p50 | - | 176-216 ms | **93-102 ms** |
| p95 server | <= 400 ms | 208-330 ms | **105-145 ms** |
| RSS, 4x rute 148 km | <= 350 MB | 767,6 MiB | 572,2 MiB (belum <=350) |

RSS beban masih di atas 350 MB untuk rute 148 km (bukan kelas rute app); untuk
42 km hanya 55 MiB. Kalau target 350 MB ingin dikejar juga untuk rute panjang,
opsinya ada di BACKLOG 6/7 (ALT atau reverse CSR sebagai sidecar, keduanya
menambah, bukan mengurangi) — atau turunkan `maxSlots` dan terima `ErrTooFar`
untuk koridor >150 km.

### M4 — Siap dipakai app (2-3 hari)

- [x] Timeout per request + `context` yang membatalkan A* di tengah jalan:
      dicek tiap 256 ekspansi, `504 {"error":"deadline exceeded"}`, workspace
      langsung dilepas. Default 10 s, flag `-timeout`.
- [x] Rate limit per IP (default 15/menit, burst 5) -> `429` + `Retry-After`.
      `-trust-proxy` supaya IP diambil dari `X-Forwarded-For`; tanpa flag itu
      header diabaikan.
- [x] `/metrics` Prometheus tanpa dependensi: `route_requests_total{code}`,
      histogram `route_request_duration_seconds`, `route_inflight`,
      `route_expansions_total`, `route_fallback_total`,
      `route_rate_limited_total`, `route_canceled_total`,
      `route_errors_total{reason}`, `routeid_build_info`.
- [x] `ETag` + `Cache-Control` untuk query identik. Tag = fingerprint graph
      (`pbf_sha256`, node/edge, commit) + query ternormalisasi, jadi urutan
      parameter dan GET-vs-POST berbagi entri cache.
- [x] `points`/`simplify` sudah terdokumentasi di README bagian Flutter.
- [ ] Bind `127.0.0.1` di belakang Caddy: **belum**, karena di homelab sekarang
      tidak ada reverse proxy untuk routeid — mengubah bind akan memutus akses
      LAN yang dipakai Postman. Resepnya ada di `deploy/README.md`.

**Selesai bila:** tidak ada request yang bisa menahan slot >10 s, dan dashboard
menampilkan RPS/p95/fallback.

**Hasil terukur (box uji yang sama, Java, `-conc 2`):**

| uji | hasil |
|---|---|
| 304 vs 200 (rute 41,7 km) | p50 **0,2 ms** vs 43,1 ms (~200x) |
| batas waktu 1 s pada rute 148 km | `504` pada 1,01 s, bukan 2,9 s |
| request pendek saat 2 slot penuh, `-timeout 1s` | selesai 813 ms |
| request pendek saat 2 slot penuh, `-timeout 20s` | selesai 2.596 ms |
| 10 request cepat, `-rate 15 -burst 5` | 5x `200`, lalu `429` + `Retry-After: 4` |
| 1 rute + 7 revalidasi (`-burst 3`) | `200 304 304 304 304 304 304 304` |
| 6 rute penuh, tag basi (`-burst 3`) | `200 200 200 429 429 429` |

Di homelab (setelah deploy `49e5279`): 304 p50 **0,6 ms** vs rute penuh 93 ms,
`Cache-Control: public, max-age=3600`, `/metrics` 48 baris.

**Temuan yang mengubah implementasi:** rate limiter semula dipasang sebelum
pengecekan `ETag`, sehingga revalidasi ikut memakai token. Sekarang limiter
dipanggil *setelah* `ETag`: hit cache tidak memakai budget rute, karena ia juga
tidak menyentuh workspace.

Artinya deadline bukan cuma mengubah status: ia mengembalikan slot ke antrean,
jadi satu klien yang meminta rute 148 km tidak lagi "menyandera" server selama
beberapa detik.

### M5 — Cakupan (3-4 hari)

- [ ] Profil motor (tabel kecepatan + `motorcycle=no`, `service` lebih permisif).
- [ ] Semua pulau besar: Kalimantan, Sulawesi, Bali-Nusra (~1 menit build masing).
- [ ] Konektor ferry (Merak-Bakauheni, Ketapang-Gilimanuk, dsb) sebagai sub-graph
      sehingga rute lintas selat tidak lagi `422`.
- [ ] Kurangi artefak: turunkan `Time` dari `Len` + kelas jalan (hemat ~187 MB di
      Java), varint untuk `GO`/`GL`.

**Selesai bila:** Jakarta -> Medan mengembalikan rute (dengan segmen ferry
ditandai), dan graph Java <= 800 MB.

### M6 — Bukti (paper)

- [ ] E1 baseline OSRM di mesin sama (`paper/EXPERIMENTS.md`).
- [ ] E2 ground truth 60-200 pasangan (`paper/COMPARISON.md` §4).
- [ ] Tabel positioning diisi angka nyata, bukan "documented".

**Selesai bila:** `paper/COMPARISON.md` tidak lagi punya baris berstatus
"belum diukur" untuk metrik inti.

## Urutan & ketergantungan

```
M1 (metadata, golden test)
 └─> M2 (snap)  ─┐
 └─> M3 (ALT)   ─┼─> M4 (produksi) ─> M5 (cakupan) ─> M6 (paper)
                 └─ M1 diperlukan agar M2/M3 bisa dibuktikan tidak meregresi
```

M1 wajib lebih dulu: tanpa golden test, M2 dan M3 mengubah rute tanpa cara
mengetahui apakah perubahannya benar.

## Risiko

| risiko | dampak | mitigasi |
|---|---|---|
| ALT memperbesar artefak 16 MB/pulau | bertentangan dengan "ringan" | 4 landmark dulu, ukur trade-off; alternatif: landmark per sel grid |
| Proyeksi ke ruas menambah seksi grid | build lebih lambat, format berubah | naikkan versi format, arsipkan artefak lama |
| Ferry connector melanggar aturan "satu region per rute" | kompleksitas | batasi hanya pasangan selat yang terdefinisi |
| Terlalu banyak fitur -> keluar dari identitas ringan | kehilangan pembeda | setiap fitur diuji terhadap tabel budget di atas |

## Catatan yang belum jadi milestone

- **Satu graph nasional** (1,66 GB pbf, ~5 GB RAM) hanya masuk akal dengan
  streaming extractor yang menulis langsung ke file; multi-region per pulau
  sudah memberi hasil yang sama dengan memori jauh lebih kecil.
- **Hierarki dua tingkat** (highway/core) baru dipertimbangkan jika ada kebutuhan
  rute >60 km atau <20 ms; CH/MLD penuh tetap ditolak.

## Cara mengukur (jangan improvisasi)

- Throughput & latensi: `scripts/loadtest.py` (kelas rute 7/15/42/148 km).
- Memori: `/proc/<pid>/status` — `RssAnon` vs `RssFile`, bukan `ps rss`.
- Regresi rute: golden 50 pasangan dari M1.
- Turn restriction: `rid-restcheck`.
- Semua angka yang masuk paper harus punya perintah reproduksi di
  `paper/RESULTS.md`, dan statusnya dicatat di `paper/COMPARISON.md`.
