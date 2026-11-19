# Benchmark

Semua angka di sini punya mesin, graph, dan bagian `paper/RESULTS.md` yang
menjadi bukti. Jangan mencampur baris dari mesin atau format graph berbeda.

## Metode

- **E1** membandingkan Nusapeta Route dan OSRM pada PBF yang sama, mesin yang sama,
  dan 600 pasangan yang sama. Kedua server dijalankan bergantian; urutan request
  dibalik per pasangan untuk mengurangi bias cache.
- 600 pasangan dibagi 120 per bucket jarak garis lurus: 0–5, 5–15, 15–30,
  30–50, 50–80 km. Generator menggeser titik tujuan dari node jalan, sehingga
  banyak target jatuh di laut/hutan.
- Pasangan dianggap **cocok** bila snap Nusapeta Route dan OSRM ≤250 m di kedua ujung
  dan titik snap kedua engine ≤50 m. Filter ini membuang perbedaan titik snap,
  bukan menyembunyikan kegagalan; jumlah yang dibuang dilaporkan.
- Latency adalah wall time HTTP, bukan waktu CPU. p50/p95 dihitung dari
  pasangan yang cocok.
- Jarak adalah jarak rute final. Selisih = `(nusapeta-route - osrm) / osrm`.
- Tidak ada cache rute pada bench; ETag/HTTP cache dimatikan.
- E1 bukan ground truth. E2 tidak dijalankan; tidak ada klaim akurasi absolut.

## Mesin

| Nama | CPU | RAM | Dipakai untuk |
|---|---|---|---|
| Ryzen | AMD Ryzen 5 5600G, 6C/12T | 16 GB | E1, kalibrasi, cap 100 km |
| i3 | Intel i3-6100T, 2C/4T | 7 GB | throughput, homelab, budget 6 s |
| i5 | Intel i5-7360U, 2C/4T | 7 GB | ε, Hilbert, snap, restriction lama |

Jangan memakai angka i5 untuk kapasitas produksi, dan jangan memakai angka
Ryzen untuk klaim throughput 2 core.

## E1: Nusapeta Route vs OSRM MLD

Mesin Ryzen, Java, PBF `java-latest.osm.pbf` 899.289.655 B, 600 pasangan.
Nusapeta Route memakai graph format 4 (22.712.775 node / 46.676.434 edge); OSRM
v5.26.0 MLD dengan `car.lua`. Ini run E1 terkontrol, bukan graph v5 nasional.

| Metrik | Nusapeta Route | OSRM |
|---|---:|---:|
| Artefak | **693.111.912 B** (1 file) | 5.082.571.256 B |
| Preprocess | **28 s** | 250 s (151 + 83 + 16) |
| Peak RSS preprocess | tidak diukur | 6.254 MB |
| RSS sebelum query | **17.280 kB** | 2.362 GiB |
| RSS setelah 600 pasangan | 815.312 kB | 2.451 GiB |
| p50, 332 pasangan cocok | 38,7 ms | **1,5 ms** |
| p95, 332 pasangan cocok | 674 ms | **3,7 ms** |
| Jarak: median \|Δ\| | 0,23% | referensi |
| Jarak: p95 \|Δ\| | 14,49% | referensi |
| Jarak: max \|Δ\| | 70,44% | referensi |
| Jarak: >10% / >25% | 25 / 8 dari 332 | — |
| Durasi: signed p50 | −1,11% | referensi |
| Durasi: signed p95 | +2,39% | referensi |
| Durasi: absolut p95 | 10,22% | referensi |

RSS Nusapeta Route setelah 600 query naik karena halaman `mmap` yang tersentuh, bukan
karena graph disalin ke heap. Angka OSRM dari `docker stats --no-stream`.

### Kelayakan pasangan

| | Jumlah |
|---|---:|
| Total pasangan | 600 |
| Cocok untuk perbandingan jarak | 332 |
| Gagal Nusapeta Route | 79 |
| Dibuang filter snap | 189 |

79 kegagalan Nusapeta Route: **72 `snap`**, **7 `budget`** (search space exhausted).
Tidak ada `no_route` pada run final. Pada 79 pasangan itu, OSRM menempel target
dengan median **15,3 km**, p90 **32,7 km**, maksimum **44,6 km** dari koordinat
input. OSRM tidak gagal, tetapi titiknya bukan pembanding yang setara.

Angka historis §13 (`21,05%` p95, `48,30%` max, 38/332) berasal dari run
sebelum kalibrasi tabel kecepatan. Jangan dipakai sebagai hasil final.

### Per bucket (final)

| Bucket | Cocok | Gagal | Nusapeta Route p50 | Nusapeta Route p95 | OSRM p50 | OSRM p95 | p95 \|Δ\| | max \|Δ\| | >10% |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 0–5 km | 86 | 0 | 4,0 ms | 13,8 ms | 0,7 ms | 1,0 ms | 16,75% | 45,17% | 9 |
| 5–15 km | 73 | 6 | 21,7 ms | 72,3 ms | 1,2 ms | 1,7 ms | 14,96% | 70,44% | 5 |
| 15–30 km | 67 | 10 | 64,8 ms | 271,1 ms | 1,6 ms | 2,3 ms | 22,73% | 35,74% | 6 |
| 30–50 km | 56 | 27 | 164,6 ms | 548,9 ms | 2,2 ms | 3,2 ms | 7,55% | 17,60% | 2 |
| 50–80 km | 50 | 36 | 525,9 ms | 1.675,3 ms | 3,2 ms | 5,1 ms | 10,12% | 22,02% | 3 |

## Heuristik dan urutan node

Data lama (Java v3, i5-7360U, 601 pasangan, 526 sukses). Tetap dicantumkan
karena menjadi dasar default `ε=1.5` dan Hilbert ordering; jangan dicampur
dengan E1.

| ε | p50 | p95 | Panjang rute identik |
|---:|---:|---:|---:|
| 1,0 | 87,8 ms | 582,6 ms | referensi |
| 1,5 | 62,3 ms | 387,6 ms | 526/526 |
| 2,0 | 44,7 ms | 222,9 ms | 525/526; 1 pasangan +11% |

ε=1,5 sekitar 28% lebih cepat dari ε=1,0 tanpa perubahan panjang pada 526
pasangan. ε=2,0 lebih cepat lagi tetapi mulai mengorbankan jarak.

Hilbert ordering vs id OSM: p50 **7–20%** lebih cepat, p95 **7–16%**, RSS
metropolitan **1.225 → 675 MB**. Efeknya locality halaman, bukan perubahan
bobot.

## Batas 100 km

Mesin Ryzen, tiga region (`java`, `sumatra`, `nusa-tenggara`), 600 pasangan
Jawa, `-rate 0`, `-conc 4 -workspace-mb 256 -timeout 20s`, tanpa cache.

| Metrik | Hasil |
|---|---:|
| Sukses / 422 | 481 / 119 |
| Wall p50 | 39–47 ms |
| Wall p95 | 0,94–1,32 s |
| Wall max | 9,5 s (rute 97,4 km sukses) |
| Wall total per run | 56–57 s |

Klasifikasi 119 kegagalan:

| Reason | Jumlah |
|---|---:|
| `snap` | 72 |
| `too_far` (jarak tempuh >100 km) | 39 |
| `budget` / search exhausted | 8 |

Per bucket pada run terakhir:

| Bucket | Sukses | p50 | p95 |
|---|---:|---:|---:|
| 0–5 km | 120/120 | 5 ms | 22 ms |
| 5–15 km | 114/120 | 30 ms | 142 ms |
| 15–30 km | 110/120 | 114 ms | 551 ms |
| 30–50 km | 91/120 | 277 ms | 917 ms |
| 50–80 km | 46/120 | 542 ms | 1,74 s |

Pita 80–100 km tidak ada di `pairs-java.csv` (berhenti di 79,9 km garis
lurus). Di i3, rute 98,4 km dan 91,8 km sekarang berhenti pada `422 budget`
6 s, bukan `504`; rute 96,4 km selesai 0,18 s.

### Budget lunak di i3

Dua instance image yang sama, `-max-route-km 100 -rate 0`, 600 pasangan:

| Setelan | 200 | `too_far` | `exhausted` | `budget` | Wall |
|---|---:|---:|---:|---:|---:|
| `-budget 6s -timeout 10s` (deploy) | 472 | 40 | 0 | 16 | 97,5 s |
| `-budget 0 -timeout 30s` (referensi) | 475 | 45 | 8 | 0 | 125,0 s |

Harga budget 6 s: 3 dari 600 pasangan (0,5%), semuanya di 7,75–9,06 s.
p99 turun 4,52 → 2,94 s; max 9,06 → 4,74 s. Menaikkan budget ke 8 s hanya
menyelamatkan satu dari tiga.

## Memori dan workspace

Graph nasional v5, Ryzen:

| Keadaan | RSS | `RssAnon` |
|---|---:|---:|
| Idle (0.1.0 image, `-conc 2 -workspace-mb 384`) | **17,8 MB** | 11,6 MB |
| Idle (0.2.0 binary, `-conc 2 -workspace-mb 384`, graph v0.2) | **14,7 MB** | 6,6 MB |
| Idle (bare binary, default flags) | 15,6 MB | 7,0 MB |
| 4× rute 96 km paralel | 225 MiB | 90 MiB |
| 4× rute 80 km paralel | 190 MiB | 90 MiB |
| 1× Jakarta–Medan 1.882 km (`-max-route-km 0`) | 701 MiB | 401 MiB |

Beban 600 pasangan cap 100 km (`-conc 4 -workspace-mb 256`): RSS 1,02 GB,
`anon` 343 MB, workspace 201 MB / 16,8 M slot dari anggaran 256 MB. Tabel
bukan penyebab kegagalan: menaikkan `-workspace-mb` ke 512/2048 tidak
memindahkan satu pun pasangan dari 422 ke 200; yang habis adalah anggaran
ekspansi.

Idle shrink: tabel 10.485.760 → 262.144 slot; `RssAnon` 143.500 → 4.532 kB.
Workspace menyusut setelah 30 detik idle, bukan menunggu restart.

## Throughput

i3-6100T, 2C/4T, satu region, `-conc 4`:

| Rute | p50 | RPS |
|---|---:|---:|
| 7 km | 47 ms | ±75 |
| 15 km | 136 ms | ±17 |
| 41,7 km | 93–102 ms | 36–40 |
| 148 km | 4,2 s | ±0,6 |

Beban campuran ≤40 km, 298 pasangan: `-conc 1` p50 24,3 ms / p95 246,2 ms;
`-conc 2` 24,7 RPS; `-conc 4` 23,5 RPS, p50 73,6 ms / p95 705,4 ms;
`-conc 8` 24,9 RPS. Di atas 4, throughput tidak naik; antrean bertambah.

## Restriction dan snapping

Java v4: 4.654 relasi restriction, 3.962 usable, 3.808 ban + 154 `only_*`,
30,1 KB; 116 rule di-relax saat build, 593 dibuang (via-way/conditional/
unresolved); 0 pelanggaran pada harness, 0 fallback saat rute. Graph v5
sekarang menyimpan ban mobil 3.694 + 154 `only_*`, motor 3.589 + 136 `only_*`.

Snapping:

| Metrik | Snap ke node | Snap ke ruas |
|---|---:|---:|
| Galat p50, segmen >150 m | 67,3 m | **16,1 m** |
| Galat p95 | 114,0 m | **34,9 m** |

Contoh Medan–Binjai: offset titik turun 45 → 4 m; jarak rute berubah −58 m.
Ini perbaikan geometri, bukan klaim akurasi rute.

## Yang tidak diukur

- **E2 ground truth** tidak dijalankan. Tidak ada klaim akurasi.
- **Latency graph nasional penuh** belum di-bench terpisah. Cap 100 km memakai
  tiga region di Ryzen; beban nasional diukur untuk memori dan route lokal,
  bukan p50/p95 satu graph 54,9 juta node.
- **OSRM di graph nasional** tidak dijalankan; perbandingan E1 hanya Java.
- **Valhalla/GraphHopper** tidak dibandingkan.
- **Traffic, musim, ferry schedule** tidak dimodelkan.
- **p99 80–100 km** belum cukup sampel; `pairs-java.csv` berhenti di 79,9 km.

## Reproduksi

```bash
# E1 (butuh OSRM di port 5000)
bin/nusa-serve -graph data/java.graph -addr 127.0.0.1:18085 \
  -conc 1 -rate 0 -timeout 60s -max-route-km 0
scripts/m6-compare.py --pairs paper/results/pairs-java.csv \
  --route http://127.0.0.1:18085 --osrm http://127.0.0.1:5000 \
  --csv paper/results/e1-osrm-aligned.csv
python3 scripts/e1-stats.py paper/results/e1-osrm-aligned.csv

# 100 km, tiga region. m6-compare.py tidak punya flag cache;
# ulangi request dari pairs tanpa ETag (lihat RESULTS.md §19).
bin/nusa-serve -graph data/java.graph -graph data/sumatra.graph \
  -graph data/nusa-tenggara.graph -addr 127.0.0.1:18085 \
  -conc 4 -rate 0 -timeout 20s -max-route-km 100 -workspace-mb 256

# load
scripts/loadtest.py http://127.0.0.1:8080 \
  -from -7.8435,110.3929 -to -7.7393,110.3760 -c 4 -n 32
```

Detail mentah, termasuk CSV dan catatan tiap run:
[`../paper/RESULTS.md`](../paper/RESULTS.md).
