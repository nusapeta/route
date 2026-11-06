# Hasil pengukuran (mentah + provenance)

Semua angka di `main.tex` berasal dari file ini. Mesin: Intel Core i5-7360U
(2C/4T, 2.3 GHz), RAM 7 GB, Linux 7.2.8, Go 1.27.3.
Data: Geofabrik `java-latest.osm.pbf` (857.6 MB) dan `sumatra-latest.osm.pbf`
(283.3 MB), diunduh 2026-11-06.

## 1. Ekstraksi

| extract | pbf | node | edge | graph | wall | peak RSS |
|---|---|---|---|---|---|---|
| Java | 857.6 MB | 22,740,189 | 46,734,283 | 1,019 MB | 58 s | 2,543 MB |
| Sumatra | 283.3 MB | 11,881,669 | 24,326,797 | 533 MB | 18 s | 1,249 MB |

```bash
./bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java.graph
./bin/rid-extract -pbf data/sumatra-latest.osm.pbf -out data/sumatra.graph
# A/B: urutan node mengikuti id OSM (bukan Hilbert)
./bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java-osm.graph -order osm
```
Peak RSS diukur dengan polling `ps -o rss` tiap 200 ms selama proses.

## 2. Latency per bucket jarak (Java, ε=1.5, 601 pasang tetap)

Workload: `paper/results/pairs-java.csv` (120 pasang per bucket jarak
straight-line). Jarak rute nyata lebih besar dari bucket karena jalan tidak lurus.

| bucket | n | rute (km) | p50 | p95 | p99 | median ekspansi | gagal |
|---|---|---|---|---|---|---|---|
| 0–5 km | 120 | 5.2 | 1.2 ms | 8.6 ms | 21.5 ms | 3,085 | 0 |
| 5–15 km | 116 | 17.5 | 16.1 ms | 71.4 ms | 244.1 ms | 40,391 | 4 |
| 15–30 km | 111 | 38.5 | 62.3 ms | 283.3 ms | 1,010.8 ms | 135,230 | 9 |
| 30–50 km | 92 | 62.6 | 145.7 ms | 472.1 ms | 671.9 ms | 314,177 | 28 |
| 50–80 km | 87 | 99.9 | 387.6 ms | 1,000.1 ms | 1,562.0 ms | 745,682 | 33 |

```bash
./bin/rid-bench -graph data/java.graph -n 120 -emit-pairs paper/results/pairs-java.csv
./bin/rid-bench -graph data/java.graph -pairs paper/results/pairs-java.csv -eps 1.5 \
  -csv paper/results/java-hilbert-eps1.5.csv
```
"Gagal" = endpoint tanpa jalan dalam radius snap 5 km (pantai, hutan, pulau kecil).

## 3. Trade-off inflasi heuristik (ε)

Pasangan identik (526 query sukses). Excess jarak dihitung per query terhadap
ε=1.0.

| ε | p50 (15–30 km) | p50 (50–80 km) | excess jarak | rasio latency p50 | p95 |
|---|---|---|---|---|---|
| 1.0 | 87.8 ms | 582.6 ms | — (optimal) | 1.00 | 1.00 |
| 1.5 | 62.3 ms | 387.6 ms | 0.000 % | 0.72 | 0.89 |
| 2.0 | 44.7 ms | 222.9 ms | mean −0.13 %, p95 0.000 %, max 11.0 % (1 pasang) | 0.52 | 0.78 |

Kesimpulan: ε=1.5 tidak mengubah satu pun panjang rute; ε=2.0 mengubah 1 dari 526.

## 4. Efek urutan node (Hilbert vs id OSM)

Workload identik, jumlah ekspansi identik (algoritma tidak berubah, hanya layout).

| bucket | p50 id | p50 Hilbert | p95 id | p95 Hilbert |
|---|---|---|---|---|
| 0–5 km | 1.5 ms | 1.2 ms | 9.6 ms | 8.6 ms |
| 5–15 km | 17.5 ms | 16.1 ms | 76.7 ms | 71.4 ms |
| 15–30 km | 67.4 ms | 62.3 ms | 332.5 ms | 283.3 ms |
| 30–50 km | 163.3 ms | 145.7 ms | 562.4 ms | 472.1 ms |
| 50–80 km | 445.7 ms | 387.6 ms | 1,185.6 ms | 1,000.1 ms |

RSS setelah replay 29 pasangan metropolitan (Jabodetabek):
`id order = 1,225 MB`, `Hilbert = 675 MB` (1.8× lebih kecil).

> Catatan: eksperimen awal (satu koridor, 4 query 50 km berulang) memberi
> 579 MB → 104 MB. Angka itu tidak apple-to-apple (koridor sama = halaman sama)
> dan **tidak** dipakai di paper.

## 5. Memori proses server

| state | RSS |
|---|---|
| start-up, 2 region | 13 MB |
| 4 query 50 km paralel | 103 MB |
| 8 query 13 km paralel (Sumatra) | 177 MB |
| 29 pasangan metropolitan (Java) | 675 MB |
| 20 pasangan acak lintas wilayah | 1,120 MB |

Semua halaman hasil `mmap` bersifat file-backed dan clean, jadi angka puncak
adalah high-water mark yang bisa direklaim kernel di bawah tekanan memori.

## 6. Multi-region (Java + Sumatra, satu proses)

| query | rute | latency | region |
|---|---|---|---|
| Medan 13 km | 23.9 km | 53 ms | sumatra.graph |
| Palembang 10.9 km | 10.9 km | 56 ms | sumatra.graph |
| Bandar Lampung 3.6 km | 3.6 km | 9 ms | sumatra.graph |
| Jakarta 56.5 km | 56.5 km | 103 ms | java.graph |

Seleksi region dilakukan dengan snap terdekat (bukan bbox saja, karena extract
per pulau saling tumpang tindih).
