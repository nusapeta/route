# Komparasi: apa yang boleh diklaim, apa yang belum

Dokumen kerja untuk Table *positioning* (`main.tex`, `tab:positioning`).
Aturan main: **hanya angka yang punya perintah reproduksi di `RESULTS.md` yang
boleh masuk kolom routeid.** Kolom pembanding (OSRM, Valhalla, GraphHopper) baru
boleh diisi setelah E1 dijalankan di mesin yang sama.

## 1. Ledger klaim

| klaim | angka | bukti | status |
|---|---|---|---|
| RSS setelah start-up | 15 MB (heap 7 / mmap 7) | `/proc/<pid>/status` setelah `/healthz` 200 | terukur |
| Waktu start sampai siap | 10 ms | polling `curl /healthz` | terukur |
| Preprocess Java | 58 s, peak 2.543 MB (1.886 MB dengan `GOMEMLIMIT=1800MiB`) | `rid-extract` + `/usr/bin/time -v` | terukur |
| Preprocess Sumatra | 23 s, peak 1.683 MB | idem | terukur |
| Graph | Java 972 MB, Sumatra 509 MB | `ls -l data/*.graph` | terukur |
| Multi-region satu proses | 2 region, 357 MB RSS | `-graph java -graph sumatra` | terukur |
| Latency 15 km | 136-155 ms | `rid-route` / HTTP | terukur |
| Latency 42 km | 191 ms (1 query) | idem | terukur |
| Throughput 42 km | 10,9 RPS @4 klien; 11,8 @8 klien | `scripts/loadtest.py` (i3-6100T) | terukur |
| Throughput 7/15/148 km | 67 / 17 / 0,59 RPS | idem | terukur |
| Memori vs beban | 15 → 89 → 385 → 607 → 1.041 MB | `/proc` per tahap | terukur |
| Turn restriction | 3.795 ban, 155 only, 587 skipped; 0 pelanggaran setelah enforcement | `rid-restcheck` | terukur |
| Fallback restriction | 3,17% | idem | terukur |
| Hilbert vs id OSM | RSS 579 → 104 MB, latency 1,5-2x | `rid-bench -order osm` | terukur |
| eps=1.5 vs 1.0 | 2,4x lebih cepat, rute identik | `rid-bench -eps` | terukur |
| Image container | 23 MB | `docker image inspect` | terukur |
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
| "siap untuk seluruh Indonesia" | baru 2 pulau, rute lintas-region ditolak |
| "hemat memori" tanpa kualifikasi | heap tumbuh mengikuti rute terpanjang x `-conc` |

## 3. Worksheet E1 (OSRM, mesin sama)

Isi setelah `osrm/osrm-backend` dijalankan pada `java-latest.osm.pbf` yang sama.
Gunakan pasangan identik: `paper/results/pairs-java.csv`.

| metrik | routeid (terukur) | OSRM MLD | OSRM CH | catatan |
|---|---|---|---|---|
| preprocess extract | 58 s |  |  | `osrm-extract` |
| preprocess contract/customize | - |  |  | |
| ukuran artefak `.osrm*` | 972 MB (1 file) |  |  | |
| RSS idle | 15 MB |  |  | |
| p50 5-15 km | 16 ms |  |  | bucket yang sama |
| p50 15-30 km | 62 ms |  |  | |
| p50 30-50 km | 146 ms |  |  | |
| p50 50-80 km | 388 ms |  |  | |
| jarak rute sama? | - |  |  | bandingkan `distance_m` per pasangan |
| turn restriction ditegakkan | ya (0 pelanggaran) |  |  | profil car.lua |

Perintah acuan (homelab punya Docker + pbf):

```bash
docker run -t -v "$PWD/data:/data" osrm/osrm-backend osrm-extract -p /opt/car.lua /data/java-latest.osm.pbf
docker run -t -v "$PWD/data:/data" osrm/osrm-backend osrm-partition /data/java-latest.osrm
docker run -t -v "$PWD/data:/data" osrm/osrm-backend osrm-customize /data/java-latest.osrm
docker run -d -p 5000:5000 -v "$PWD/data:/data" osrm/osrm-backend osrm-routed --algorithm mld /data/java-latest.osrm
# pasangan sama, urutan OSRM = lon,lat
python3 paper/results/pairs-java.csv  ->  curl "http://127.0.0.1:5000/route/v1/driving/{lon1},{lat1};{lon2},{lat2}?overview=false"
```

Peringatan RAM: `osrm-extract` untuk Java butuh ~3-4 GB. Di homelab (sisa ~3,5 GB
+ 4 GB swap) jalankan sendiri-sendiri, jangan bersamaan dengan rebuild graph.

## 4. Worksheet E2 (ground truth)

Butuh referensi independen; OSRM bukan ground truth (dua-duanya graf OSM).

| metode | jumlah sampel | yang dicatat | biaya |
|---|---|---|---|
| Google Maps manual | 60 pasangan | jarak rute + waktu, screenshot | ~2 jam kerja |
| Google Directions API | 200-500 pasangan, tersebar | `distance`, `duration`, `polyline` | perlu API key + biaya kecil |

Metrik yang dilaporkan: selisih jarak relatif `|d_routeid - d_ref| / d_ref`,
rasio detour, dan persentase rute dengan selisih >10%. Tanpa ini, klaim
"akurat" tetap tidak boleh muncul di abstrak.
