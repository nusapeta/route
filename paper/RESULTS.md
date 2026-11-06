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

Satu proses, dua region (java + sumatra), `-conc 4`. Diukur dari
`/proc/<pid>/status` setelah beban tertentu (bukan estimasi):

| state | RSS | RssAnon | RssFile |
|---|---|---|---|
| start-up | 15 MB | 7 MB | 7 MB |
| 20x rute 15 km | 89 MB | 73 MB | 15 MB |
| 1x rute 148 km | 385 MB | 281 MB | 103 MB |
| 1x rute 578 km | 607 MB | 442 MB | 164 MB |
| 29x rute 148 km | 1,041 MB | 876 MB | 165 MB |

Puncak **bukan** halaman mmap. Yang tumbuh adalah heap Go (`RssAnon`): ruang
kerja A* (open set open-addressing berisi `keys`, `gen`, `gs`, `came`, 16 B per
slot) membesar mengikuti rute terpanjang yang pernah dilayani, lalu di-pool per
slot konkurensi dan **tidak menyusut**. `RssFile` (halaman graph hasil `mmap`)
tetap kecil karena hanya sebagian file yang tersentuh dan sifatnya clean —
bagian itu saja yang bisa direklaim kernel.

Konsekuensinya untuk deployment: anggaran memori ditentukan rute terpanjang ×
`-conc`, bukan ukuran file graph. Knob yang terukur:

| konfigurasi | puncak RSS |
|---|---|
| java saja, `-conc 4` | 312 MB |
| java + sumatra, `-conc 4` | 558 MB |
| java + sumatra, `-conc 1` | 524 MB |
| java + sumatra, `-conc 4`, `GOMEMLIMIT=300MiB` | 424 MB |

### 5b. Run di homelab (i3-6100T, 4 thread, 7.8 GB, Ubuntu 24.04)

Deploy nyata: container 1 GB limit, `-conc 1`, `GOMEMLIMIT=768MiB`, graph
dibangun di mesin itu sendiri (peak build 1,886 MB untuk Java dengan
`GOMEMLIMIT=1800MiB`, 1,683 MB untuk Sumatra).

| query | rute | latency |
|---|---|---|
| Yogyakarta | 14.7 km | 155 ms |
| Jakarta -> Bandung | 148.5 km | 4.18 s |
| Palembang | 7.3 km | 57 ms |
| Medan -> Binjai | 22.7 km | 86 ms |
| Banda Aceh -> Medan | 578.7 km | 2.25 s |

Memori container setelah semua query di atas: **471 MiB** dari limit 1 GiB
(`docker stats`), RAM host terpakai 3,812 MB dari 7,830 MB. Latency Jakarta ->
Bandung 16% lebih lambat dari mesin uji (3.59 s), sesuai rasio clock.

Throughput (`scripts/loadtest.py`, keep-alive, 24–80 request per konfigurasi):

| rute | `-conc` | klien c | RPS | server p50 |
|---|---|---|---|---|
| 7 km | 4 | 4 | 74.6 | 47 ms |
| 7 km | 4 | 8 | 72.9 | 79 ms |
| 15 km | 1 | 1 | 7.1 | 135 ms |
| 15 km | 2 | 4 | 12.6 | 243 ms |
| 15 km | 4 | 4 | 16.9 | 226 ms |
| 148 km | 4 | 4 | 0.59 | 6,649 ms |

Pada kelas rute yang dibatasi maksimum 40 km (Jakarta -> Cikarang, 41.977 m):

| `-conc` | klien c | RPS | server p50 | p95 klien |
|---|---|---|---|---|
| 1 | 1 | 4,97 | 191 ms | 224 ms |
| 2 | 2 | 8,72 | 219 ms | 251 ms |
| 4 | 4 | 10,93 | 326 ms | 439 ms |
| 4 | 8 | 11,76 | 628 ms | 744 ms |

Saturasi ~11–12 RPS. Rata-rata pemakaian CPU 10,9 x 0,326 s = 3,6 core-ekuivalen,
yaitu seluruh 2 core fisik + SMT; menambah container kedua pada mesin yang sama
tidak menaikkan throughput, hanya menambah antrean.

Saturasi tercapai pada 4 query paralel: 2 core fisik, tiap query memakai satu
core penuh. Menambah konkurensi klien di atas itu hanya memperpanjang antrean
(p50 15 km naik 226 -> 486 ms tanpa kenaikan RPS). Memori puncak pada 4x rute
148 km paralel: 745 MiB, tanpa OOM pada limit 1.5 GiB.

## 6. Multi-region (Java + Sumatra, satu proses)

| query | rute | latency | region |
|---|---|---|---|
| Medan 13 km | 23.9 km | 53 ms | sumatra.graph |
| Palembang 10.9 km | 10.9 km | 56 ms | sumatra.graph |
| Bandar Lampung 3.6 km | 3.6 km | 9 ms | sumatra.graph |
| Jakarta 56.5 km | 56.5 km | 103 ms | java.graph |

Seleksi region dilakukan dengan snap terdekat (bukan bbox saja, karena extract
per pulau saling tumpang tindih).

## 7. Turn restriction (v2)

Ekstraksi relasi `type=restriction` dari `java-latest.osm.pbf`:

| kategori | jumlah |
|---|---|
| relasi `type=restriction` | 4.653 |
| usable (from+to way ada di profil mobil) | 4.203 (90,3%) |
| via-node (berhasil di-resolve) | 2.808 |
| via-way (berhasil di-resolve) | 1.258 |
| pasangan terlarang (`no_*`) | 3.794 |
| aturan izin (`only_*`) | 155 |
| gagal: no_in / no_out / no_node | 11 / 38 / 1 |
| gagal: rantai via-way / `only_*` + via-way | 29 |
| tidak dimodelkan: conditional / except / bad_type / not_car | 2 / 16 / 184 / 227 |

Ukuran tambahan pada graf: 3.949 pasangan × 8 B = **31,6 KB** (0,003% dari
1.019 MB). Alternatif CSR penuh akan butuh 8 B × 46,7 jt edge = 374 MB.

Kepatuhan (`bin/rid-restcheck -graph data/java.graph`, seluruh 3.564 pasangan
yang bisa dirutekan, mesin yang sama):

| | pelanggaran | fallback | error | mean latensi |
|---|---|---|---|---|
| strict (default) | **0 (0,00%)** | 113 (3,17%) | 0 | 81,2 ms |
| longgar (tanpa restriction) | **3.414 (95,79%)** | 0 | 0 | 2 µs |

`non_vacuous = 3.414 / 3.564 = 95,79%`: pada 95,79% kasus, rute terpendek
memang **ingin** mengambil belokan terlarang — jadi uji ini tidak vakum.
Fallback = kasus di mana larangan (umumnya via-way konservatif) membuat tujuan
tak terjangkau; router mengulang tanpa restriction dan menandai
`"fallback": true`.

Biaya latensi (Java, ε=1.5, 60 pasang per bucket, strict vs longgar):

| bucket | strict p50 | longgar p50 | strict p95 | longgar p95 |
|---|---|---|---|---|
| 0–5 km | 4,2 ms | 3,9 ms | 21,9 ms | 59,6 ms |
| 5–15 km | 45,7 ms | 28,5 ms | 299,1 ms | 302,2 ms |
| 15–30 km | 165,0 ms | 151,9 ms | 882,7 ms | 807,8 ms |
| 30–50 km | 792,7 ms | 712,6 ms | 2.494,7 ms | 2.246,6 ms |

Ekspansi A* praktis sama (±0,1%): pengecekan larangan hanya menambah satu
binary search 13 langkah pada transisi yang benar-benar dibatasi.

Biaya build (VmHWM, polling `/proc/<pid>/status` tiap 200 ms):

| konfigurasi | peak RSS | wall |
|---|---|---|
| default (termasuk pass restriction) | 3.209 MB | 1 m 26 s |
| `GOMEMLIMIT=2200MiB` | 2.160 MB | 58 s |
| `GOMEMLIMIT=1800MiB` | 1.807 MB | 62 s |

Catatan: angka 2.543 MB pada §1 diukur dengan sampling `ps` yang melewatkan
puncak; VmHWM adalah angka yang benar.

```bash
./bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java.graph
./bin/rid-restcheck -graph data/java.graph -json paper/restrcheck-java.json
./bin/rid-bench -graph data/java.graph -no-restrictions   # baseline A/B
```
