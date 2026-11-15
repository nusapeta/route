# Komparasi: apa yang boleh diklaim, apa yang belum

> Catatan rename 2026-11-15: perintah di bawah memakai nama pra-rename
> (`rid-*`); padanan nama sekarang ada di [`README.md`](README.md).


Dokumen kerja untuk Table *positioning* (`main.tex`, `tab:positioning`).
Aturan main: **hanya angka yang punya perintah reproduksi di `RESULTS.md` yang
boleh masuk kolom routeid.** Kolom OSRM MLD sudah terisi dari E1 (mesin sama);
Valhalla/GraphHopper/OSRM CH masih butuh run sendiri sebelum masuk tabel.

## 1. Ledger klaim

| klaim | angka | bukti | status |
|---|---|---|---|
| RSS setelah start-up | **17.280 kB** (E1 Java); **11,1 MB** graph nasional (`anon` 4,5 MB) | `/proc/<pid>/status` setelah `/healthz` 200 | terukur |
| Waktu start sampai siap | 10 ms | polling `curl /healthz` | terukur |
| Preprocess Java (v3, 1 worker) | 58 s, peak 2.543 MB (1.886 MB dengan `GOMEMLIMIT=1800MiB`) | `rid-extract` + `/usr/bin/time -v` | terukur |
| Preprocess Sumatra | 23 s, peak 1.683 MB | idem | terukur |
| Graph (v5) | Java **693 MB**, Sumatra 353 MB, Nusa 149 MB; nasional **1.621 MB** (satu artefak mobil+motor) | `ls -l data/*.graph` | terukur |
| Beban graph nasional | 4× rute 96 km paralel: 225 MiB (`anon` 90 MiB); 1× 1.882 km: 701 MiB (`anon` 401 MiB) | `/proc/<pid>/status` | terukur |
| Latency 15 km | 136-155 ms | `rid-route` / HTTP | terukur |
| Latency 42 km | p50 **93–102 ms**, p95 **105–145 ms** (i3, `-conc 4`) | `scripts/loadtest.py` | terukur |
| Throughput 42 km | **36–40 RPS** @4 klien (i3-6100T) | `scripts/loadtest.py` | terukur |
| Throughput 7/15/148 km | **±75** / ±17 / ±0,6 RPS (i3-6100T, 4 klien) | idem | terukur |
| Memori vs beban | 15 -> 399 MB (4x 148 km serentak, v4) | `/proc` per tahap | terukur |
| Turn restriction | 4.654 relasi, **3.962 usable** (3.808 ban + 154 `only_*`), 593 di-skip; v5 mobil 3.694 ban + 154 `only_*` | `rid-restcheck` | terukur |
| Fallback restriction | **0%** (116 ban dilonggarkan saat build) | idem | terukur |
| Hilbert vs id OSM | RSS metropolitan **1.225 → 675 MB** (1,8x), p50 7-20 % lebih cepat | `rid-bench -order osm` | terukur |
| eps=1.5 vs 1.0 | **1,4x** lebih cepat (rasio p50 0,72), rute identik 526/526 | `rid-bench -eps` | terukur |
| Image container | **36,7 MB** (multi-stage); prebuilt belum diukur ulang | `docker image inspect` | terukur |
| Galat snap ruas >150 m | p50 16,1 m / p95 34,9 m (node: 67,3 / 114,0) | `rid-snapcheck -bbox ... -offset 40` | terukur |
| SnapEdge vs oracle | mean −1,25 m, 0% sampel >1 m lebih buruk | idem | terukur |
| Koreksi ujung rute | Medan–Binjai: offset 45→4 m, jarak −58 m, `trimmed=both` | `GET /route` + `X-Route-Trim` | terukur |
| E1 latency vs OSRM MLD (332 pasang cocok) | routeid p50 **38,7 ms**, p95 **674 ms**; OSRM **1,5 ms / 3,7 ms** | `scripts/m6-compare.py` (Ryzen 5 5600G) | terukur |
| E1 jarak vs OSRM | median **0,23%**, p95 **14,49%**, max 70,44%; 25/332 >10% | idem, filter snap <=250 m & gap <=50 m | terukur, gerbang p95 dan median gagal |
| E1 durasi vs OSRM (D4) | signed p50 **-1,11%**, signed p95 **+2,39%**, absolut p95 **10,22%** | idem | terukur, metrik primer 2026-11-10 |
| Penalti belok (0,25x default, sebelum kalibrasi) | mean \|Δ\| 3,60% -> **3,50%**, p75 3,03% -> 2,82%, p90 11,20% -> 10,56%, p95 **21,12% tetap**; 111/600 rute berubah, ETA +1,6% median | `rid-server -turn-penalty 0/0,25/1/2` + harness E1 | terukur, `RESULTS.md` §15 |
| E1 RSS | start **17.280 kB**, setelah 600 pasangan **815.312 kB**; OSRM 2,362/2,451 GiB | `/proc`, `docker stats` | terukur |
| E1 artefak & preprocess | **693.111.912 B / 28 s** vs **5.082.571.256 B / 250 s** (extract+partition+customize) | `du -sb`, `scripts/m6-osrm.sh extract` | terukur |
| E1 cakupan | 79/600 routeid `422` (72 snap, 7 budget); OSRM 0 error, snap tujuan pada 79 itu p50 15,3 km | idem | terukur |

Catatan M2: galat snapping sudah terukur (ledger di atas), tetapi itu galat
menempelkan titik ke jalan, bukan bukti bahwa jarak rute benar. Klaim akurasi
rute dicabut: E2 tidak dijalankan (D1 2026-11-10: opsi limitation).

## 2. Yang tidak boleh diklaim sekarang

| klaim | kenapa |
|---|---|
| "lebih cepat dari OSRM" | E1 sudah jalan: OSRM menang latency (p50 1,5 ms vs 38,7 ms; p95 3,7 ms vs 674 ms) |
| "lebih akurat" | E2 tidak dijalankan (D1: limitation). E1 hanya perbandingan antar-engine; p95 selisih jarak 14,49% (gerbang 12% gagal) |
| "RSS lebih kecil dari OSRM" tanpa angka | terukur (17 MB vs 2,36 GiB idle; 815 MB vs 2,45 GiB setelah 600 pasangan), tetapi harus menyebut mesin, beban, dan bahwa RSS memuat halaman mmap plus tabel workspace yang tidak mengecil |
| "siap untuk seluruh Indonesia" | graph nasional 1.621 MB sudah ada; rute >100 km tetap ditolak `too_far`/`budget` |
| "hemat memori" tanpa kualifikasi | tabel workspace 12 B/slot tumbuh mengikuti edge unik rute terpanjang, dikali region x `-conc`, dan bertahan sampai restart; E1: 815 MB setelah 600 pasangan campuran |

## 3. Worksheet E1 (OSRM MLD, mesin sama), terisi

Run final: 2026-11-10, AMD Ryzen 5 5600G (6C/12T), 16 GB RAM, 30 GiB swap.
PBF dan set pasangan identik (`paper/results/pairs-java.csv`, 600 pasang).
Hanya 332 pasang masuk statistik jarak: snap kedua engine <=250 m dan titik snap
kedua engine <=50 m di kedua ujung. Rincian: `RESULTS.md` §13 (historis) dan §18 (final).

| metrik | routeid (E1) | OSRM MLD | OSRM CH | catatan |
|---|---|---|---|---|
| preprocess extract | 28 s (`GOMEMLIMIT=2GiB`) | 151 s, peak 6.254 MB | tidak dijalankan | 1 file vs pipeline 3 tahap |
| preprocess partition/customize | - | 83 s + 16 s (peak 3.798/2.350 MB) | | routeid tanpa tahap ini |
| total preprocess | **28 s** | **250 s** | | ~9x |
| ukuran artefak | **693.111.912 B** (693 MB) | **5.082.571.256 B** (4,73 GiB) | | 7,3x; `.osrm*` banyak file |
| RSS sebelum query | 17.280 kB | 2,362 GiB | | `docker stats`; mmap ikut terhitung |
| RSS setelah 600 pasangan | 815.312 kB | 2,451 GiB | | puncak bukan diukur serentak; didominasi tabel workspace, bukan halaman graf |
| p50 0-5 km | 4,0 ms | 0,7 ms | | 86 pasang |
| p50 5-15 km | 21,7 ms | 1,2 ms | | 73 pasang |
| p50 15-30 km | 64,8 ms | 1,6 ms | | 67 pasang |
| p50 30-50 km | 164,6 ms | 2,2 ms | | 56 pasang |
| p50 50-80 km | 525,9 ms | 3,2 ms | | 50 pasang |
| p95 0-5/…/50-80 km | 13,8 / 72,3 / 271,1 / 548,9 / 1.675,3 ms | 1,0 / 1,7 / 2,3 / 3,2 / 5,1 ms | | |
| jarak rute: median / p95 / max | **0,23% / 14,49% / 70,44%** | referensi | | gerbang p95 <12% dan median <0,1% **gagal**, tetapi bukan gerbang akurasi (§18) |
| durasi: signed p50 / signed p95 / absolut p95 | **-1,11% / +2,39% / 10,22%** | referensi | | metrik primer D4 (§18) |
| pasangan selisih >10% | 25/332 (7,5%) | n/a | | 8/332 >25% |
| routeid 422 | 79/600 (72 snap, 7 budget) | 0 error | | OSRM snap tujuan p50 15,3 km pada 79 itu |
| turn restriction ditegakkan | 3.808 ban + 154 `only_*`, 593 di-skip | `car.lua` | | audit `RESULTS.md` §14: bukan penyebab p95 jarak |
| penalti belok | 0,25x tabel (2/7,5/20 s), ETA +1,6% median | `car.lua` `weight` | | `RESULTS.md` §15: p95 tidak turun, badan distribusi membaik |

Catatan metrik: selisih jarak bukan ukuran akurasi. Audit `RESULTS.md` §14
menunjukkan 20 outlier berasal dari perbedaan fungsi biaya (routeid meminimalkan
waktu murni termasuk `track`; OSRM `weight` = durasi + penalti belok +
preferensi kelas jalan), bukan dari restriction yang di-skip. Penalti belok
sudah ditambahkan (default 0,25x, `RESULTS.md` §15): p95 tidak berubah, sisa
selisih berasal dari tabel kecepatan kelas. Angka durasi juga tidak sebanding
(tabel kecepatan kelas vs `car.lua` maxspeed), jadi tidak dipakai sebagai klaim.

Isi dengan `scripts/m6-compare.py` (jarak + latency per bucket + snap) dan
`scripts/m6-osrm.sh extract` (waktu + peak RSS tiap tahap); angka di atas dari
run final `paper/results/e1-osrm-aligned.csv`.

Perintah acuan sudah dibungkus jadi skrip; resep lengkap di `docs/M6.md`:

```bash
scripts/m6-osrm.sh extract data/java-latest.osm.pbf   # + wall & peak RSS tiap tahap

# serve: container detached (mode service gagal exit 1 di mesin ini)
sudo -u arifsani -g docker docker run -d --name osrm-routed -p 5000:5000 \
  -v "$PWD/data:/data" osrm/osrm-backend:latest \
  osrm-routed --algorithm mld /data/java-latest.osrm

# routeid di mesin yang sama, conc 1
bin/rid-server -graph data/java.graph -addr 127.0.0.1:8080 -conc 1 -rate 0 -timeout 60s

# 600 pasangan identik, diukur bergantian (OSRM lon,lat ditangani skrip)
scripts/m6-compare.py --pairs paper/results/pairs-java.csv \
  --routeid http://127.0.0.1:8080 --osrm http://127.0.0.1:5000 \
  --max-snap 250 --max-gap 50 --csv paper/results/e1-osrm-aligned.csv
```

Peringatan RAM: `osrm-extract` Jawa terukur **peak 6.254 MB**; mesin 16 GB cukup,
mesin 8 GB wajib swap. Jangan dijalankan bersamaan dengan `rid-extract` atau
load test: waktu dan RSS jadi sampah. E1 dijalankan di Ryzen 5 5600G; belum
dicoba di i5-7360U 7,8 GB.

Catatan metodologi: OSRM MLD di sini memakai `car.lua` (maxspeed), routeid
memakai tabel kecepatan per kelas jalan, jadi **durasi tidak sebanding**.
Yang dibandingkan: jarak rute (akurasi relatif), latency, memori, ukuran artefak.

## 4. Worksheet E2 (ground truth)

Butuh referensi independen; OSRM bukan ground truth (dua-duanya graf OSM).

| metode | jumlah sampel | yang dicatat | biaya |
|---|---|---|---|
| Google Maps manual | 60 pasangan | jarak rute + waktu, screenshot | ~2 jam kerja |
| Google Routes API | 200-500 pasangan, tersebar | `distanceMeters`, `duration`, `polyline` | perlu API key (~$1 untuk 200-500 query) |

Status: **tidak dijalankan**. D1 pada 2026-11-10 memilih opsi limitation; tidak
ada API key dan tidak ada data referensi. Limitation eksplisit ditulis di
abstrak + `main.tex` §Threats~(ii): tidak ada klaim akurasi. Urutan kerja dan
definisi selesai ada di `docs/M6.md` §E2.

Metrik yang dilaporkan: selisih jarak relatif `|d_routeid - d_ref| / d_ref`,
rasio detour, dan persentase rute dengan selisih >10%. Dengan D1, klaim
"akurat" tidak boleh muncul di abstrak.
