# Komparasi: apa yang boleh diklaim, apa yang belum

Dokumen kerja untuk Table *positioning* (`main.tex`, `tab:positioning`).
Aturan main: **hanya angka yang punya perintah reproduksi di `RESULTS.md` yang
boleh masuk kolom routeid.** Kolom pembanding (OSRM, Valhalla, GraphHopper) baru
boleh diisi setelah E1 dijalankan di mesin yang sama.

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
| Throughput 7/15/148 km | 67 / 17 / 0,59 RPS | idem | terukur |
| Memori vs beban | 15 -> 399 MB (4x 148 km serentak, v4) | `/proc` per tahap | terukur |
| Turn restriction | 3.795 ban, 155 only, 587 skipped; 0 pelanggaran setelah enforcement | `rid-restcheck` | terukur |
| Fallback restriction | 3,17% | idem | terukur |
| Hilbert vs id OSM | RSS 579 → 104 MB, latency 1,5-2x | `rid-bench -order osm` | terukur |
| eps=1.5 vs 1.0 | 2,4x lebih cepat, rute identik | `rid-bench -eps` | terukur |
| Image container | 24 MB (prebuilt, alpine) | `docker image inspect` | terukur |
| Galat snap ruas >150 m | p50 16,1 m / p95 34,9 m (node: 67,3 / 114,0) | `rid-snapcheck -bbox ... -offset 40` | terukur |
| SnapEdge vs oracle | mean −1,25 m, 0% sampel >1 m lebih buruk | idem | terukur |
| Koreksi ujung rute | Medan–Binjai: offset 45→4 m, jarak −58 m, `trimmed=both` | `GET /route` + `X-Route-Trim` | terukur |

Catatan M2: galat snapping sudah terukur (ledger di atas), tetapi itu galat
menempelkan titik ke jalan — bukan bukti bahwa jarak rute benar. Klaim akurasi rute
tetap menunggu E2.

## 2. Yang **tidak** boleh diklaim sekarang

| klaim | kenapa |
|---|---|
| "lebih cepat dari OSRM" | tidak ada run head-to-head (E1) |
| "lebih akurat" | tidak ada ground truth (E2); yang terukur baru galat *attachment*, bukan galat jarak rute |
| "RSS lebih kecil dari OSRM" | angka OSRM di paper masih dari dokumentasi |
| "siap untuk seluruh Indonesia" | tujuh pulau sudah terbangun, tapi rute antar-file masih ditolak (`422`) kecuali satu graph nasional 1.650 MB dipakai |
| "hemat memori" tanpa kualifikasi | heap tumbuh mengikuti rute terpanjang x `-conc` |

## 3. Worksheet E1 (OSRM, mesin sama)

Isi setelah `osrm/osrm-backend` dijalankan pada `java-latest.osm.pbf` yang sama.
Gunakan pasangan identik: `paper/results/pairs-java.csv`.

| metrik | routeid (terukur, v4) | OSRM MLD | OSRM CH | catatan |
|---|---|---|---|---|
| preprocess extract | 3m53s (4 worker, box berbeban) |  |  | `osrm-extract` |
| preprocess contract/customize | - (tanpa tahap ini) |  |  | |
| ukuran artefak | **693 MB** (1 file) |  |  | `.osrm*` = banyak file |
| RSS idle | 12,8 MB (2 region) |  |  | |
| RSS @4x148 km, `-conc 4` | 399 MB |  |  | `docker stats` |
| p50 5-15 km | 16 ms |  |  | bucket sama; §2 diukur di v3 |
| p50 15-30 km | 62 ms |  |  | v4 ~1,6-2x lebih cepat (§12b) |
| p50 30-50 km | 146 ms |  |  | idem |
| p50 50-80 km | 388 ms |  |  | idem |
| jarak rute sama? | - |  |  | `m6-compare.py` mengisi ini |
| turn restriction ditegakkan | ya (0 pelanggaran) |  |  | profil car.lua |

Isi dengan `scripts/m6-compare.py` (mengisi jarak + latency per bucket) dan
`scripts/m6-osrm.sh extract` (waktu + peak RSS tiap tahap).

Perintah acuan — sudah dibungkus jadi skrip, resep lengkap di `docs/M6.md`:

```bash
scripts/m6-osrm.sh extract data/java-latest.osm.pbf   # + wall & peak RSS tiap tahap
scripts/m6-osrm.sh serve   data/java-latest.osrm      # osrm-routed MLD :5000

# routeid di mesin yang sama, conc 1
bin/rid-server -graph data/java.graph -addr 127.0.0.1:8080 -conc 1 -rate 0 -timeout 60s

# 601 pasangan identik, diukur bergantian (OSRM lon,lat ditangani skrip)
scripts/m6-compare.py --pairs paper/results/pairs-java.csv \
  --routeid http://127.0.0.1:8080 --osrm http://127.0.0.1:5000 \
  --csv paper/results/m6-osrm-vs-routeid.csv
```

Peringatan RAM: `osrm-extract` untuk Jawa butuh ~4-6 GB. Mesin 8 GB wajib punya
swap, dan jangan dijalankan bersamaan dengan `rid-extract` / load test — angka
waktu dan RSS jadi sampah. Laptop pengembangan (i5-7360U, 7,8 GB) dinilai tidak
memadai; homelab ditolak karena 28 container lain membuat latency bising.

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
bisa dipakai sebagai pengganti (dua-duanya graf OSM). Urutan kerja dan definisi
selesai ada di `docs/M6.md` §E2.

Metrik yang dilaporkan: selisih jarak relatif `|d_routeid - d_ref| / d_ref`,
rasio detour, dan persentase rute dengan selisih >10%. Tanpa ini, klaim
"akurat" tetap tidak boleh muncul di abstrak.
