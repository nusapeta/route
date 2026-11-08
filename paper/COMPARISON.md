# Komparasi: apa yang boleh diklaim, apa yang belum

Dokumen kerja untuk Table *positioning* (`main.tex`, `tab:positioning`).
Aturan main: **hanya angka yang punya perintah reproduksi di `RESULTS.md` yang
boleh masuk kolom routeid.** Kolom OSRM MLD sudah terisi dari E1 (mesin sama);
Valhalla/GraphHopper/OSRM CH masih butuh run sendiri sebelum masuk tabel.

## 1. Ledger klaim

| klaim | angka | bukti | status |
|---|---|---|---|
| RSS setelah start-up | **12,8 MB** (Java+Sumatra, v4); 14,9 MB untuk tiga region | `/proc/<pid>/status` setelah `/healthz` 200 | terukur |
| Waktu start sampai siap | 10 ms | polling `curl /healthz` | terukur |
| Preprocess Java (v3, 1 worker) | 58 s, peak 2.543 MB (1.886 MB dengan `GOMEMLIMIT=1800MiB`) | `rid-extract` + `/usr/bin/time -v` | terukur |
| Preprocess Sumatra | 23 s, peak 1.683 MB | idem | terukur |
| Graph (v4) | Java **693 MB**, Sumatra 364 MB; motor 682/349 MB; nasional 1.650 MB | `ls -l data/*.graph` | terukur |
| Multi-region satu proses | 3 region, idle 14,9 MB; puncak 399 MB pada 4x 148 km (`-conc 4`) | `-graph java -graph sumatra -graph nusa-tenggara` | terukur |
| Latency 15 km | 136-155 ms | `rid-route` / HTTP | terukur |
| Latency 42 km | 191 ms (v3, 1 query) -> v4 ~1,6x lebih cepat; p50 server 223-283 ms di homelab berbeban | idem + `paper/RESULTS.md` §12b | terukur |
| Throughput 42 km | v3 9,97-10,58 RPS -> v4 **13,79-16,02 RPS** @4 klien (A/B bergantian) | `scripts/loadtest.py` (i3-6100T) | terukur |
| Throughput 7/15/148 km | **74,6** / 16,9 / 0,59 RPS (i3-6100T, 4 klien) | idem | terukur |
| Memori vs beban | 15 -> 399 MB (4x 148 km serentak, v4) | `/proc` per tahap | terukur |
| Turn restriction | **3.790 ban / 593 di-skip** (build `5d3fdd1b`); 0 pelanggaran setelah enforcement | `rid-restcheck` | terukur |
| Fallback restriction | 3,17% | idem | terukur |
| Hilbert vs id OSM | RSS metropolitan **1.225 → 675 MB** (1,8x), p50 7-20 % lebih cepat | `rid-bench -order osm` | terukur |
| eps=1.5 vs 1.0 | **1,4x** lebih cepat (rasio p50 0,72), rute identik 526/526 | `rid-bench -eps` | terukur |
| Image container | 24 MB (prebuilt, alpine) | `docker image inspect` | terukur |
| Galat snap ruas >150 m | p50 16,1 m / p95 34,9 m (node: 67,3 / 114,0) | `rid-snapcheck -bbox ... -offset 40` | terukur |
| SnapEdge vs oracle | mean −1,25 m, 0% sampel >1 m lebih buruk | idem | terukur |
| Koreksi ujung rute | Medan–Binjai: offset 45→4 m, jarak −58 m, `trimmed=both` | `GET /route` + `X-Route-Trim` | terukur |
| E1 latency vs OSRM MLD (332 pasang cocok) | routeid p50 **35,1 ms**, p95 **522 ms**; OSRM **1,4 ms / 3,4 ms** | `scripts/m6-compare.py` (Ryzen 5 5600G) | terukur |
| E1 jarak vs OSRM | median **−0,005%**, p95 **21,05%**, max 48,30%; 38/332 >10% | idem, filter snap <=250 m & gap <=50 m | terukur, gerbang p95 gagal |
| Penalti belok (0,25x default) | mean \|Δ\| 3,60% -> **3,50%**, p75 3,03% -> 2,82%, p90 11,20% -> 10,56%, p95 **21,12% tetap**; 111/600 rute berubah, ETA +1,6% median | `rid-server -turn-penalty 0/0,25/1/2` + harness E1 | terukur, `RESULTS.md` §15 |
| E1 RSS | start **17.280 kB**, setelah 600 pasangan **815.312 kB**; OSRM 2,362/2,451 GiB | `/proc`, `docker stats` | terukur |
| E1 artefak & preprocess | **693.111.912 B / 28 s** vs **5.082.571.256 B / 250 s** (extract+partition+customize) | `du -sb`, `scripts/m6-osrm.sh extract` | terukur |
| E1 cakupan | 81/600 routeid `422` (72 snap >3 km); OSRM 0 error, snap tujuan pada 81 itu p50 14,7 km | idem | terukur |

Catatan M2: galat snapping sudah terukur (ledger di atas), tetapi itu galat
menempelkan titik ke jalan — bukan bukti bahwa jarak rute benar. Klaim akurasi rute
tetap menunggu E2.

## 2. Yang **tidak** boleh diklaim sekarang

| klaim | kenapa |
|---|---|
| "lebih cepat dari OSRM" | E1 sudah jalan: OSRM menang latency (p50 1,4 ms vs 35 ms; p95 3,4 ms vs 522 ms) |
| "lebih akurat" | E2 belum ada ground truth. E1 hanya perbandingan antar-engine; p95 selisih jarak 21% (gerbang 12% gagal) |
| "RSS lebih kecil dari OSRM" tanpa angka | terukur (17 MB vs 2,36 GiB idle; 815 MB vs 2,45 GiB setelah 600 pasangan), tetapi harus menyebut mesin, beban, dan bahwa RSS memuat halaman mmap plus tabel workspace yang tidak mengecil |
| "siap untuk seluruh Indonesia" | tujuh pulau sudah terbangun, tapi rute antar-file masih ditolak (`422`) kecuali satu graph nasional 1.650 MB dipakai |
| "hemat memori" tanpa kualifikasi | tabel workspace 12 B/slot tumbuh mengikuti edge unik rute terpanjang, dikali region x `-conc`, dan bertahan sampai restart; E1: 815 MB setelah 600 pasangan campuran |

## 3. Worksheet E1 (OSRM MLD, mesin sama) — terisi

Run: 2026-11-07, AMD Ryzen 5 5600G (6C/12T), 16 GB RAM, 30 GiB swap.
PBF dan set pasangan identik (`paper/results/pairs-java.csv`, 600 pasang).
Hanya 332 pasang masuk statistik jarak: snap kedua engine <=250 m dan titik snap
kedua engine <=50 m di kedua ujung. Rincian: `RESULTS.md` §13.

| metrik | routeid (E1) | OSRM MLD | OSRM CH | catatan |
|---|---|---|---|---|
| preprocess extract | 28 s (`GOMEMLIMIT=2GiB`) | 151 s, peak 6.254 MB | tidak dijalankan | 1 file vs pipeline 3 tahap |
| preprocess partition/customize | - | 83 s + 16 s (peak 3.798/2.350 MB) | | routeid tanpa tahap ini |
| total preprocess | **28 s** | **250 s** | | ~9x |
| ukuran artefak | **693.111.912 B** (693 MB) | **5.082.571.256 B** (4,73 GiB) | | 7,3x; `.osrm*` banyak file |
| RSS sebelum query | 17.280 kB | 2,362 GiB | | `docker stats`; mmap ikut terhitung |
| RSS setelah 600 pasangan | 815.312 kB | 2,451 GiB | | puncak bukan diukur serentak; didominasi tabel workspace, bukan halaman graf |
| p50 0-5 km | 3,6 ms | 0,7 ms | | 86 pasang |
| p50 5-15 km | 19,7 ms | 1,1 ms | | 73 pasang |
| p50 15-30 km | 54,7 ms | 1,7 ms | | 67 pasang |
| p50 30-50 km | 139,0 ms | 2,2 ms | | 56 pasang |
| p50 50-80 km | 397,3 ms | 3,2 ms | | 50 pasang |
| p95 0-5/…/50-80 km | 14,0 / 68,7 / 226,7 / 478,4 / 1.060,9 ms | 1,1 / 1,7 / 2,3 / 3,0 / 4,5 ms | | |
| jarak rute: median / p95 / max | **−0,005% / 21,05% / 48,30%** | referensi | | gerbang p95 <12% **gagal**, tetapi bukan gerbang akurasi (§14) |
| pasangan selisih >10% | 38/332 (11,4%) | n/a | | 10/332 >25% |
| routeid 422 | 81/600 (72 snap, 7 budget, 2 no-route) | 0 error | | OSRM snap tujuan p50 14,7 km pada 81 itu |
| turn restriction ditegakkan | 3.790 ban, 593 di-skip | `car.lua` | | audit `RESULTS.md` §14: bukan penyebab p95 jarak |
| penalti belok | 0,25x tabel (2/7,5/20 s), ETA +1,6% median | `car.lua` `weight` | | `RESULTS.md` §15: p95 tidak turun, badan distribusi membaik |

Catatan metrik: selisih jarak bukan ukuran akurasi. Audit `RESULTS.md` §14
menunjukkan 38 outlier berasal dari perbedaan fungsi biaya (routeid meminimalkan
waktu murni termasuk `track`; OSRM `weight` = durasi + penalti belok +
preferensi kelas jalan), bukan dari restriction yang di-skip. Penalti belok
sudah ditambahkan (default 0,25x, `RESULTS.md` §15): p95 tidak berubah, sisa
selisih berasal dari tabel kecepatan kelas. Angka durasi juga tidak sebanding
(tabel kecepatan kelas vs `car.lua` maxspeed), jadi tidak dipakai sebagai klaim.

Isi dengan `scripts/m6-compare.py` (jarak + latency per bucket + snap) dan
`scripts/m6-osrm.sh extract` (waktu + peak RSS tiap tahap); angka di atas dari
run terakhir yang menghasilkan `paper/results/m6-osrm-vs-routeid.csv`.

Perintah acuan — sudah dibungkus jadi skrip, resep lengkap di `docs/M6.md`:

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
  --max-snap 250 --max-gap 50 --csv paper/results/m6-osrm-vs-routeid.csv
```

Peringatan RAM: `osrm-extract` Jawa terukur **peak 6.254 MB**; mesin 16 GB cukup,
mesin 8 GB wajib swap. Jangan dijalankan bersamaan dengan `rid-extract` atau
load test — waktu dan RSS jadi sampah. E1 dijalankan di Ryzen 5 5600G; belum
dicoba di i5-7360U 7,8 GB.

Catatan metodologi: OSRM MLD di sini memakai `car.lua` (maxspeed), routeid
memakai tabel kecepatan per kelas jalan — jadi **durasi tidak sebanding**.
Yang dibandingkan: jarak rute (akurasi relatif), latency, memori, ukuran artefak.

## 4. Worksheet E2 (ground truth)

Butuh referensi independen; OSRM bukan ground truth (dua-duanya graf OSM).

| metode | jumlah sampel | yang dicatat | biaya |
|---|---|---|---|
| Google Maps manual | 60 pasangan | jarak rute + waktu, screenshot | ~2 jam kerja |
| Google Routes API | 200-500 pasangan, tersebar | `distanceMeters`, `duration`, `polyline` | perlu API key (~$1 untuk 200-500 query) |

Status: **belum ada** — tidak ada API key di mesin pengembangan, dan OSRM tidak
bisa dipakai sebagai pengganti (dua-duanya graf OSM). Limitation eksplisit sudah
ditulis di abstrak + `main.tex` §Threats~(ii): tidak ada klaim akurasi. Urutan
kerja dan definisi selesai ada di `docs/M6.md` §E2.

Metrik yang dilaporkan: selisih jarak relatif `|d_routeid - d_ref| / d_ref`,
rasio detour, dan persentase rute dengan selisih >10%. Tanpa ini, klaim
"akurat" tetap tidak boleh muncul di abstrak.
