# Roadmap routeid

**North star:** alternatif OSRM / Google Routes yang paling ringan:
router jalan Indonesia yang memakai puluhan MB, dan tetap
menjawab rute <=50 km di bawah setengah detik di perangkat murah.

Rencana eksekusi aktif (memori + akurasi, terukur 2026-11-08):
`docs/PLAN-resource-accuracy.md`.

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
| RSS idle (3 region termuat, v4) | **14,9 MB** | <= 20 MB | 32 MB |
| Binary statis | 6,6 MB | <= 8 MB | 12 MB |
| Image container | 24 MB (prebuilt alpine) | <= 25 MB | 40 MB |
| Start sampai `/healthz` 200 | 10 ms | <= 25 ms | 100 ms |
| Preprocess per pulau | 58 s (v3, 1 worker) / **3m53s** (v4 Java, 4 worker, box berbeban) | <= 90 s di mesin sepi | 5 menit |
| RSS @ beban 42 km, `-conc 4` | 558 MB -> **958 MB** (3 region nyata; 1,21 GB graf ter-`mmap`) / anon **311 MB** | <= 350 MB anon | 700 MB anon |
| Throughput 42 km (i3-6100T) | 10,9 (v3) -> **13,8-16,0 RPS** (v4) | >= 20 RPS | - |
| Densitas | 19,5 RPS/GB (v3) -> **~240 RPS/GB** (v4: 15 RPS / 64 MB) | >= 45 RPS/GB | - |
| Artefak graph per pulau | **693 MB** (Java, v4; -32 %) | <= 800 MB | 1 GB |
| p95 klien 42 km, `-conc 4` | 330 ms (v4, homelab berbeban) | <= 400 ms ✅ | 800 ms |
| Galat ujung rute (snap) | **p50 16 m, p95 36 m** (offset uji <=40 m) | p50 <= 20 m, p95 <= 60 m | p95 150 m |
| Pelanggaran turn restriction | 0 (0% fallback, 116 ban dilonggarkan saat build) | 0 (fallback <= 1%) | 0 |

"Densitas" = RPS / RSS, metrik utama proyek ini: satu GB RAM harus menampung
sebanyak mungkin rute per detik.

## Status ringkas (2026-11-10)

| milestone | status | hasil inti |
|---|---|---|
| M1 metadata + golden test | ✅ | format v3/v4 provenance, `/version`, regresi otomatis |
| M2 snap ke ruas | ✅ | galat ujung 67,3 -> 16,1 m p50 |
| M3 biaya per ekspansi | ✅ | latensi ~2x, memori -25 % |
| M4 produksi | ✅ (bind LAN ditunda) | deadline, rate limit, ETag (304 p50 0,2 ms Ryzen / 0,39 ms i3-6100T), `/metrics` |
| M5 cakupan | ✅ | v4 (Java 693 MB), ferry, motor, 7 pulau, tangga ε |
| M6 bukti (paper) | 🟡 | E1 OSRM selesai (Ryzen, `RESULTS.md` §13); E2 dilewati (D1: limitation eksplisit) |

Ground truth E2 tidak dikerjakan: D1 pada 2026-11-10 memilih opsi limitation.
E1 sudah terukur ulang: median jarak 0,23%, p95 14,49% (gerbang 12% gagal);
D4 mengganti metrik primer ke p95 durasi absolut 10,22%. Audit `RESULTS.md`
§18 menunjukkan selisih berasal dari fungsi biaya, akses/restriksi, dan pilihan
rute, bukan restriction yang di-skip. README/paper tetap tidak boleh mengklaim
"tercepat" maupun "paling akurat", hanya biaya per rute (memori, artefak,
latency) dan perbandingan antar-engine.

## Milestone

### M1. Bisa dipercaya (SELESAI)

Tujuan: artefak bisa diaudit dan regresi ketahuan otomatis. Tanpa ini, angka
performa tidak bisa dibandingkan antar build.

- [x] Metadata graph format **v3**: SHA-256 pbf + waktu build di header, blob
      `key=value` di akhir file (`source`, `pbf_sha256`, `pbf_bytes`,
      `extractor`, `profile`, `profile_sha256`, `max_speed_kmh`, `order`,
      `grid_step`, `restrictions`).
      Terbaca di `/healthz` dan `rid-graphinfo`; `/version` menyebut binary.
      v1/v2 tetap dimuat.
- [x] Build info di binary via `-ldflags` (`main.commit`, `main.buildTime`,
      `extract.Version`), otomatis dari `make build` / `make static`.
- [x] Golden test 48 pasangan pada kota sintetis 30x30, plus round-trip
      provenance dan uji kompatibilitas v2.
- [x] Biaya diukur: hash pass menambah ~0,3 s dari 21,2 s total ekstraksi
      Sumatra (peak 1.661 MB, sama seperti sebelumnya); blob metadata 288 byte.

**Terbukti:** `format: v3`, `pbf_sha256: dcbc563f…` di `/healthz`; mengubah satu
kecepatan di test menggagalkan 3 pasangan (harness tidak vakum).

### M2. Akurasi ujung rute (SELESAI)

- [x] `SnapEdge` (proyeksi ke ruas) + `RouteSnap` (trim bila rute keluar lewat
      ruas itu, spur bila tidak). Tanpa perubahan format, tanpa pseudo-node.
- [x] `/route` dan `rid-route` memakai jalur ruas; respons memuat `snap_from`,
      `snap_to`, `trimmed`, header `X-Route-Trim`; `&pin=1` = perilaku lama.
- [x] `rid-snapcheck`: node vs ruas vs oracle, bucket panjang ruas, opsi `-bbox`.
- [x] Angka di `RESULTS.md` §9, golden test memuat 48 pasangan snap.

**Hasil:** ruas >150 m: 67,3 → 16,1 m p50 (p95 114,0 → 34,9 m). Offset pin Medan
45 → 4 m; jarak rute terkoreksi 58 m; geometri mulai/berakhir di jalan.
**Sisa:** kasus spur (rute keluar arah lain) masih garis lurus sepanjang `Along`.

### M3. Naikkan densitas 2x (SELESAI)

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
(815k vs 818k); kemenangannya murni biaya per ekspansi. Waktu tempuh 537/537
pasang identik; 1 pasang memilih geometri lain dengan waktu sama.

**Tidak dikerjakan (dengan alasan terukur):**

- ALT ditunda: `d(l,·)` untuk 4 landmark sebagai `uint16` tetap butuh
  2 x 4 x 2 B x 22,7 jt node = 182 MB untuk Java, dan bidirectional butuh
  reverse CSR ~278 MB, keduanya melewati batas 350 MB sebelum workspace.
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
menambah, bukan mengurangi), atau turunkan `maxSlots` dan terima `ErrTooFar`
untuk koridor >150 km.

### M4. Siap dipakai app (SELESAI; bind LAN ditunda)

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
      tidak ada reverse proxy untuk routeid; mengubah bind akan memutus akses
      LAN yang dipakai Postman. Resepnya ada di `deploy/README.md`.

**Selesai bila:** tidak ada request yang bisa menahan slot >10 s, dan dashboard
menampilkan RPS/p95/fallback.

**Hasil terukur (box uji yang sama, Java, `-conc 2`):**

| uji | hasil |
|---|---|
| 304 vs 200 (rute 41,7 km) | p50 **0,2 ms** (Ryzen 5 5600G) vs 43,1 ms (~200x) |
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
tidak menyentuh workspace. Konsekuensinya validator harus mahal dijaga: `ETag`
dan `Cache-Control: public` hanya dipasang pada `200`/`304`, sementara semua
respons error (`400`/`422`/`429`/`500`/`504`) membuang `ETag` dan memakai
`Cache-Control: no-store`, supaya klien yang di-throttle tidak bisa memanen tag
dari `429` dan semua respons membawa `Vary: Accept-Encoding`.

Artinya deadline mengembalikan slot ke antrean, jadi satu klien yang meminta rute
148 km tidak lagi "menyandera" server selama beberapa detik.

### M5. Cakupan (SELESAI; stitching antar-file selesai dengan catatan)

- [x] Profil motor (`-profile motor`): tabel kecepatan sendiri, `motorcycle=no`
      menang atas apa pun, `motor_vehicle=no` tidak memblokir motor (motor
      bukan motor_vehicle), tol dibuang kecuali `motorcycle=yes`, dan
      `path`/`footway`/`cycleway` boleh dipakai kalau ditandai untuk motor.
- [x] Semua pulau besar: Java, Sumatra, Kalimantan, Sulawesi, Bali-Nusra,
      Maluku, Papua, satu perintah (`make islands` / `make motor`).
- [x] Ferry jadi edge biasa dengan waktu dari tag `duration`
      (`Merak-Bakauheni` 2 jam, bukan 30 km / 20 km/jam), ditandai di graph
      sebagai daftar edge sparse dan diekspos di respons (`ferry_m`,
      `has_ferry`, `ferry_spans`).
- [x] Format v4 memangkas artefak: `Off` uint32 (−4 B/node), `Len` uint16
      satuan 4 m (−2 B/edge), section `Time` dihapus dan diganti `Cls` uint8
      (−3 B/edge). Total −12 B/node-edge: Java 1.019 → **693 MB**.
- [x] Stitching antar-file dengan tabel connector statis
      (Merak-Bakauheni, Ketapang-Gilimanuk), `legs[]`, dan
      `route_cross_region_stitched_total`. Pasangan tak terdaftar tetap `422`.
      Default `-max-route-km 40` tidak diubah; rute antarpulau memakai flag
      eksplisit `-max-route-km 0`. Jakarta -> Medan masih `relaxed=true` pada
      leg Sumatra, dicatat sebagai batas W6.

**Hasil terukur:**

| region | node | edge | v3 | v4 | build (4 worker) |
|---|---|---|---|---|---|
| Java | 22,7 M | 46,7 M | 1.019 MB | **693 MB** | 3m53s |
| Sumatra | 11,9 M | 24,3 M | 533 MB | **364 MB** | 1m09s |
| Kalimantan | n/a | n/a | n/a | **215 MB** | 51s |
| Sulawesi | n/a | n/a | n/a | **173 MB** | 45s |
| Bali-Nusra | n/a | n/a | n/a | **153 MB** | 51s |
| Maluku | n/a | n/a | n/a | **25 MB** | 8s |
| Papua | n/a | n/a | n/a | **34 MB** | 8s |

Tujuh region = 1.657 MB (v3 setara ~2,4 GB). Anggaran 800 MB berlaku per
artefak yang dimuat: region terbesar (Java) 693 MB, dan subset yang realistis
untuk satu box (Java + Bali-Nusra) 846 MB.

**Akurasi & kecepatan v4** (rute sama, graph sama, box uji sama):

| rute | v3 | v4 | selisih |
|---|---|---|---|
| Jakarta-Bandung 148 km | 148.536 m / 6424,0 s / 5,46 s | 148.673 m / 6428,4 s / **2,71 s** | +0,09% / +0,07% |
| Jakarta-Cikarang 42 km | 42.304 m / 1938,3 s / 442 ms | 42.369 m / 1941,1 s / **260 ms** | +0,15% / +0,14% |
| Bandung-Cimahi 13 km | 13.058 m / 773,7 s / 53 ms | 13.100 m / 775,2 s / **29 ms** | +0,32% / +0,19% |

Kuantisasi 4 m dan kelas 0,5 km/h menggeser jarak <0,35%; yang lebih penting,
pencarian jadi ~2x lebih cepat karena `Off` uint32 memangkas trafik memori dan
satu section hilang.

**Ferry dan rute lintas selat.** Ferry jadi edge biasa, jadi tidak perlu
sub-graph kalau kedua ujung ada di satu graph. Bila ujungnya berbeda file,
handler memakai tabel connector statis, merutekan per leg, lalu menggabungkannya
menjadi satu polyline dengan `legs[]`.

| query | hasil | ferry |
|---|---|---|
| Ketapang -> Gilimanuk (`java.graph`) | 15,1 km / 1436 s | `ferry_m=4180`, `ferry_spans=[[321,329]]` |
| Merak -> Bakauheni (graph nasional) | 29,2 km / 5226 s | `ferry_m=28535`, `ferry_spans=[[1,25]]` |
| Jakarta -> Medan (graph nasional) | **2004,5 km / 92.963 s** | `ferry_m=29072`, `relaxed=true` |

**Temuan yang mengubah implementasi: tangga ε saat budget habis.** Koridor
>800 km dulu berhenti di `ErrTooFar` (Palembang -> Medan sudah gagal di v2/v3,
jadi ini bukan regresi v4). Sekarang `Route` mencoba ulang dengan heuristik yang
lebih rakus sebelum menyerah: ε 1,5 -> 2,0 -> 3,0, dan hasilnya ditandai
`Relaxed` (+ `X-Route`/`/metrics`). Ongkosnya terukur di Jakarta-Bandung:
ε 1,5 = 148,67 km; ε 2,0 = 148,62 km (+0,07% jarak, +1,8% waktu, 5,6 jt -> 103 rb
ekspansi); ε 2,5 = 155,3 km (+4,4%). Jadi 2,0 murah, 3,0 hanya untuk lintas
pulau. Jakarta -> Medan butuh ε 3,0: 4,8 jt ekspansi, 19-22 s di box uji (default
`-timeout 10s` akan memotongnya, jadi `-timeout 60s` untuk kelas ini).

Graph nasional: `data/indonesia.graph`, 53,9 M node / 110,2 M edge, **1.650 MB**,
8m08s dengan `GOMEMLIMIT=4GiB` di box 7 GB (workers 3). Artefak ini tidak untuk
dideploy per-default (budget 800 MB), tapi membuktikan konektivitas 7 region.

**Catatan pengukuran:** box uji M5 ini dipakai bersama (4 core, load 3-5), jadi
angka absolutnya lebih lambat dari §2/RESULTS. Yang diukur adalah rasio A/B
bergantian: ekspansi identik, CPU per query turun **1,6-2x** (Jakarta-Bandung
18,6-22,1 s -> 11,7-13,7 s; Jakarta-Cikarang 2,2-2,4 s -> 0,95-1,54 s).

### M6. Bukti (paper): E1 SELESAI (2026-11-07), E2 DILEWATI (D1: limitation)

Resep lengkap: **`docs/M6.md`**. E1 dijalankan di `omarchy` (Ryzen 5 5600G,
6C/12T, 16 GB) dengan OSRM v5.26.0 MLD vs routeid, PBF Java yang sama, 600
pasangan identik. Hasil: `paper/RESULTS.md` §13; worksheet:
`paper/COMPARISON.md` §3.

Terukur: preprocess 28 s vs 250 s; artefak 693 MB vs 5,08 GB; RSS sebelum query
17 MB vs 2,36 GiB; p50 35,1 ms vs 1,4 ms; p95 522 ms vs 3,4 ms. Jarak (332
pasang setelah filter snap): median 0,23%, p95 14,49%, max 70,44%; 81/600
routeid `422` (72 karena snap >3 km) dan pada pasangan itu OSRM menempel ke
target p50 14,7 km. Gerbang p95 <12% dan median <0,1% tidak lulus; D4 memakai
p95 durasi absolut 10,22% sebagai metrik primer. Tidak ada klaim akurasi.

- [x] Audit p95: 593 restriction di-skip + 38 outlier (`paper/RESULTS.md` §14);
      hasilnya perbedaan fungsi biaya, bukan restriction; 7 `restriction:motorcar`
      diperbaiki.
- [x] Penalti belok 0,25x sebagai default + E1 ulang 0/0,25/1/2x
      (`paper/RESULTS.md` §15): mean 3,60% -> 3,50%, p95 tetap 21,12%, ETA
      +1,6%; sisa selisih dari tabel kecepatan kelas (`BACKLOG.md` 16c).
- [x] E1 baseline OSRM MLD di mesin sama (`docs/M6.md`).
- [x] E2 diputuskan dilewati (D1, 2026-11-10): tidak ada API key dan OSRM bukan
      ground truth; keterbatasan dinyatakan eksplisit di abstrak dan §Limitations.
- [x] Tabel positioning diisi angka nyata, bukan "documented".
- [x] Deploy build penalti belok ke homelab (graf Java/Sumatra dibangun ulang,
      Bali-Nusra dari PBF baru) + kapasitas pada batas desain 40 km
      (`paper/RESULTS.md` §16: p50 24,3 ms / p95 246,2 ms `-conc 1`, plafon
      24-25 RPS, p95 jarak 7,44%).

**Selesai bila:** `paper/COMPARISON.md` tidak lagi punya baris berstatus
"belum diukur" untuk metrik inti, setiap klaim di README punya angka
pendukungnya, dan E2 diganti limitation eksplisit (D1).

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
- **Peta coba publik** (`BACKLOG.md` 18): halaman statis yang di-embed ke
  gateway untuk mencoba rute dari browser. Belum menjadi milestone sampai ada
  waktu deploy dan keputusan sumber tile.

## Cara mengukur (jangan improvisasi)

- Throughput & latensi: `scripts/loadtest.py` (kelas rute 7/15/42/148 km).
- Memori: `/proc/<pid>/status`: `RssAnon` vs `RssFile`, bukan `ps rss`.
- Regresi rute: golden 50 pasangan dari M1.
- Turn restriction: `rid-restcheck`.
- Semua angka yang masuk paper harus punya perintah reproduksi di
  `paper/RESULTS.md`, dan statusnya dicatat di `paper/COMPARISON.md`.
