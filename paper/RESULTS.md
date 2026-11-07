# Hasil pengukuran (mentah + provenance)

Semua angka di `main.tex` berasal dari file ini. Kecuali §13, mesin: Intel Core
i5-7360U (2C/4T, 2.3 GHz), RAM 7 GB, Linux 7.2.8, Go 1.27.3. §13 (E1 vs OSRM)
diukur di AMD Ryzen 5 5600G; lihat provenance di sana.
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

## 8. Provenance & regresi (format v3)

Rebuild Sumatra dengan `rid-extract` v3 (`GOMEMLIMIT=1800MiB`), diukur di mesin
yang sama:

| metrik | v2 (sebelum) | v3 (sesudah) |
|---|---|---|
| wall ekstraksi | 23,1 s | **21,2 s** |
| peak RSS | 1.683 MB | 1.661 MB |
| ukuran graph | 533.012.816 B | 533.013.104 B (+288 B blob) |
| nodes / edges | 11.881.669 / 24.326.797 | identik |

Hash pass (SHA-256 atas 283 MB pbf) berada di dalam noise pengukuran; biayanya
jauh di bawah satu persen waktu build. Artefak sekarang menyebut dirinya sendiri:

```
$ bin/rid-graphinfo -graph data/sumatra.graph
format     : v3
pbf sha256 : dcbc563fb5433d714faecef21359ef3be80bb1abb69d9a65d346b72d58339831
built at   : 2026-11-07T01:18:49Z
source     : sumatra-latest.osm.pbf
pbf_bytes  : 283307196
extractor  : 16de793
profile_sha256 : 62c6c8adad06d546357e1c9126e2a13746444c2096f6d7df880a632100908aa9
order      : hilbert
restrictions : on
```

`/healthz` mengulang field yang sama per region dan `/version` menyebut commit +
waktu build binary, sehingga pertanyaan "binary dan graph mana yang jalan di box
itu" bisa dijawab tanpa masuk ke dalam container.

**Harness regresi.** Kota sintetis 30x30 (1.800 node, 3.480 edge, kecepatan
per-baris 5/8 m/s) dibangun di dalam test, ditulis ke disk, di-`mmap`, lalu 48
pasangan di-snap dan dirutekan; hasilnya dipin per pasangan (`dist_m`, `time_ds`,
jumlah node, hash FNV). Eksperimen kontrol: mengubah satu kecepatan dari 8,0 ke
8,5 m/s menggagalkan 3 dari 48 pasangan, jadi test ini benar-benar menjaga
perilaku, bukan sekadar lulus.

## 9. Akurasi snapping: node vs ruas (M2)

`rid-snapcheck -graph data/sumatra.graph -bbox -0.6,100.0,0.4,101.0 -n 5000 -offset 40`
— 5.000 titik simulasi digeser tegak lurus 0–40 m dari sebuah ruas (Sumatra barat,
1.020.007 edge dalam bbox):

| panjang ruas | n | node p50 | node p95 | ruas p50 | ruas p95 |
|---|---|---|---|---|---|
| ≤20 m | 3.382 | 17,2 m | 36,8 m | 15,8 m | 36,1 m |
| 20–50 m | 1.094 | 20,8 m | 38,7 m | 15,7 m | 37,0 m |
| 50–150 m | 477 | 31,9 m | 53,2 m | 18,6 m | 36,7 m |
| >150 m | 47 | 67,3 m | 114,0 m | **16,1 m** | **34,9 m** |

Bacaan yang benar: galat "ruas" kira-kira sama dengan offset injeksi (0–40 m),
artinya titik benar-benar jatuh di jalan; galat node menambahkan setengah panjang
ruas di atasnya. Median panjang ruas di Sumatra hanya 11 m (graf padat karena
setiap node OSM menjadi node graf), jadi keuntungan terbesar muncul di jalan cepat
yang ruasnya panjang: **67,3 → 16,1 m (4,2×)** di bucket >150 m.

Verifikasi implementasi: `SnapEdge` dibanding oracle brute-force (jarak titik ke
semua ruas di sekitar) memberi **mean −1,25 m** dan **0,00% sampel** lebih buruk
>1 m; p99 oracle 68,0 m vs 39,3 m pada implementasi (oracle memakai grid kasar).

Efek pada rute nyata (Medan–Binjai, 22,7 km):

| | node snap (sebelum) | ruas snap (sesudah) |
|---|---|---|
| offset titik awal | 45 m | **4 m** |
| offset titik akhir | 27 m | 24 m |
| jarak rute | 22.679 m | 22.621 m |
| geometri ujung | pin mentah → jalan (garis lurus) | mulai/berakhir di jalan |
| `trimmed` | – | `both` (41 m + 17 m terpotong) |
| titik polyline | 567 | 565 (+2 dengan `&pin=1`) |

Biaya latensi dari snapping ruas tidak terukur (20 request per konfigurasi, keep-alive):

| rute | node snap p50 | ruas snap p50 |
|---|---|---|
| Palembang 7 km | 27 ms | 27 ms |
| Medan–Binjai 23 km | 59 ms | 60 ms |

Respons memuat `snap_from`/`snap_to` (koordinat proyeksi) dan header
`X-Route-Trim`, sehingga app bisa menaruh pin di posisi mentah dan polyline di
jalan tanpa garis spur yang menyeberang bangunan.

## 10. Biaya per ekspansi: heuristik + workspace + heap (M3)

Profil CPU rute 30–50 km (Java, ε=1,5, `rid-bench -cpuprofile`) sebelum M3:

| fungsi | flat |
|---|---|
| `workspace.probe` (hash slot) | 28,1% |
| `math.sin` + `math.cos` + `math.xatan` (via `Haversine`) | 27,0% |
| `heapPop` | 12,2% |
| `sort.Search` (cek turn restriction per tetangga) | 4,4% |

Yang diubah, semuanya di jalur panas tanpa menyentuh format graph:

1. **Heuristik tanpa trigonometri.** `hctx` menyimpan koordinat tujuan sekali per
   query; estimasi per relaksasi jadi satu lookup tabel kosinus (resolusi 1/20°,
   dibulatkan ke bawah) + satu `sqrt`, memakai skala meter teraman
   (`110540` m/° lintang, `111320·cos(max|lat|)` m/° bujur) dan margin 0,99.
   Selisih flat-earth vs great-circle orde dua (≈0,15% pada 10°, 1,4% pada 30°),
   jadi heuristik tetap batas bawah—dibuktikan `TestHeuristicAdmissible`.
2. **Workspace 12 B/slot.** Kunci dan cap generasi berbagi satu `uint32`
   (`gen<<nodeBits | edge+1`), nodeBits dari jumlah edge (Java 25 bit → 7 bit
   generasi, tabel dibersihkan tiap 128 query, biaya ~0,05 ms/query). Sebelumnya
   kunci dan generasi adalah dua array terpisah (16 B/slot).
3. **Turn restriction sekali per ekspansi.** `Graph.Span(e)` mengembalikan rentang
   `ban`/`only` lewat dua `sort.Search`, lalu pengecekan tetangga linear—bukan
   empat pencarian biner per ekspansi seperti sebelumnya.
4. **Heap 4-ary** dan load factor tabel 0,7 → 0,8.

Hasil, dua binary dijalankan bergantian pada 91 pasang yang sama (30–50 km),
`-eps 1.5`, tiga ronde:

| ronde | p50 lama | p50 baru | rasio per-pasangan (median) |
|---|---|---|---|
| 1 | 406 ms | 206 ms | 1,9x |
| 2 | 523 ms | 254 ms | 1,95x |
| 3 | 401 ms | 242 ms | 1,76x |

Throughput lewat `rid-server -conc 2`, satu rute 41,7 km, `scripts/loadtest.py
-c 2 -n 12` (dua ronde, angka ronde 2):

| | lama | baru | rasio |
|---|---|---|---|
| RPS | 23,5 | **41,5** | 1,76x |
| p50 server | 79 ms | **45 ms** | 1,76x |
| p95 server | 88 ms | 52 ms | 1,69x |

Catatan metodologi: box uji ini (2 core/4 thread) mengalami throttling saat uji
panjang—p50 lama bisa bergeser 400→800 ms antar-runs, jadi yang bermakna adalah
rasio yang diukur bergantian, bukan angka absolut.

**Ekspansi hampir tidak berubah** (rata-rata 815.579 → 817.551, +0,2% karena
heuristik sedikit lebih longgar). Artinya kemenangan 1,9–2,0x datang murni dari
biaya per ekspansi, bukan dari pencarian yang lebih pintar—persis yang tidak
bisa dicapai ALT tanpa tambahan memori (lihat ROADMAP M3).

**Kualitas rute**: pada 601 pasang Java, waktu tempuh **537/537 sama** (hanya
pasangan yang berhasil dibandingkan, 64 gagal di kedua versi); 1 pasang memilih
geometri lain dengan waktu tempuh identik. Golden test kota sintetis di-rebaseline:
10 entri berubah jarak/waktu <=8% pada rute 250 m–3,4 km, 8 di antaranya jadi
lebih pendek (ε=1,5 memang tidak menjamin optimal; kedua versi sama-sama
suboptimal).

Puncak memori workspace ikut turun: tabel maksimum 2^25 slot, 12 B/slot =
**403 MB/query** (sebelumnya 537 MB/query).

### 10b. Konfirmasi di homelab (i3-6100T, 4 thread, container 1,5 GiB)

Deploy nyata: `routeid:local` diganti build `45de2d0` (commit tercatat di
`/version`), `-conc 4`, `GOMEMLIMIT=1GiB`. Image lama disimpan sebagai
`routeid:pre-m3` dan dijalankan berdampingan di port lain supaya A/B-nya
sekaligus mesin, graph, dan page cache.

Rute 41,7 km (`-7.685807,111.504115` -> `-7.968214,111.468762`),
`scripts/loadtest.py -c 4 -n 20`, dua binary diukur bergantian:

| ronde | pre-m3 RPS | m3 RPS | pre-m3 p50 | m3 p50 |
|---|---|---|---|---|
| 1 | 15,84 | 35,73 | 216 ms | 99 ms |
| 2 | 18,79 | 40,08 | 181 ms | 93 ms |
| 3 | 21,70 | 39,89 | 176 ms | 95 ms |

Sesudah promosi (container hidup, tiga ronde lagi): **36,1 / 38,9 / 38,6 RPS**,
p50 96-102 ms, p95 105-123 ms — naik **~2,05x** dari median pre-m3 (18,8 RPS).
p95 400 ms yang jadi target M4 sudah lewat jauh di angka ini.

Memori terukur (`docker stats`, setelah beban yang sama):

| beban | pre-m3 | m3 | delta |
|---|---|---|---|
| 4x rute 148 km Jakarta-Bandung, `-conc 4` | 767,6 MiB | **572,2 MiB** | -25,5% |
| 4x rute 41,7 km, `-conc 4` | 70,9 MiB | 54,8 MiB | -22,8% |

Penghematan 25% itu persis rasio slot 12/16 B; puncak 148 km turun dari 75%
limit 1,5 GiB ke 56%, jadi ada ruang untuk `-conc` lebih tinggi atau graph
ketiga.

Cek jawaban lintas-versi di box yang sama (rute berbeda, satu request):

| rute | pre-m3 | m3 | selisih |
|---|---|---|---|
| Medan-Binjai | 22.684 m / 1215,6 s | 22.656 m / 1212,5 s | -28 m / -0,26% |
| Palembang 4,6 km | 4.564 m / 233,5 s | 4.560 m / 232,7 s | -4 m / -0,34% |
| Jakarta-Cikarang | 40.659 m / 1940,8 s | 40.646 m / 1934,7 s | -13 m / -0,31% |
| Jakarta-Bandung | 148.518 m / 6423,7 s | 148.523 m / 6424,0 s | +5 m / +0,00% |

Rute 148 km (Jakarta-Bandung) juga ikut membaik: p50 5.038 -> 3.657 ms (1,38x),
karena biaya per ekspansi yang turun tetap terasa walau frontier-nya besar.

## 11. Cache, deadline, rate limit (M4)

Semua diuji di box uji yang sama (Java, `-conc 2`), lalu di homelab setelah
deploy.

**304 vs 200.** `ETag` = fingerprint graph (`pbf_sha256`, jumlah node/edge,
commit) + hash query ternormalisasi, jadi urutan parameter dan GET-vs-POST
menghasilkan tag yang sama.

| permintaan | p50 | bytes |
|---|---|---|
| `200` rute 41,7 km | 43,1 ms | 3,7 kB |
| `304` (`If-None-Match` cocok) | **0,2 ms** | 0 |

Sekitar 200x lebih murah, dan yang lebih penting: request 304 tidak pernah
menyentuh workspace A*, jadi app yang mengulang query saat rotasi layar tidak
lagi ikut menghabiskan slot.

**Deadline mengembalikan slot, bukan cuma mengganti status.** Dua rute 148 km
dijalankan paralel memenuhi kedua workspace, lalu satu rute 41,7 km datang:

| `-timeout` | rute 148 km | rute pendek (antre) |
|---|---|---|
| 20 s | selesai 2.942 / 2.777 ms | selesai **2.596 ms** |
| 1 s | `504` pada 1.062 / 1.011 ms | selesai **813 ms** |

Tanpa deadline, rute pendek menunggu pekerjaan panjang selesai; dengan deadline
ia hanya menunggu sampai slot dikembalikan. Ini alasan `-conc` tidak perlu
dinaikkan untuk melindungi ekor latency.

**Rate limit.** `-rate 15 -burst 5`, sepuluh request cepat dari satu IP:

```
200 200 200 200 200 429 429 429 429 429   (Retry-After: 4)
```

IP lain tidak terpengaruh (diuji di `TestLimiterWrap`), dan `X-Forwarded-For`
hanya dipercaya kalau `-trust-proxy` diberikan.

**/metrics.** 48 baris, tanpa dependensi: jumlah request per kode, histogram
latency (bucket 10 ms..10 s + `+Inf`), `route_inflight`, `route_expansions_total`,
`route_fallback_total`, `route_rate_limited_total`, `route_canceled_total`,
`route_errors_total{reason=too_far|no_route|snap|cross_region}`, dan
`routeid_build_info`.

**Batas waktu di dalam A*.** Pengecekan `ctx.Err()` dilakukan tiap 256 ekspansi:
satu pop ~200 ns, jadi biayanya di bawah 0,05% dan pembatalan terjadi dalam
hitungan milidetik. `TestRouteCanceled` membatalkan setelah 3 pengecekan dan
memastikan `ErrCanceled` (bukan `ErrTooFar`) serta workspace tetap bisa dipakai
query berikutnya.

### 11b. Konfirmasi di homelab

Deploy `a3be2f9` ke homelab (`routeid:local`, image lama disimpan
`routeid:pre-m4`), flag `-timeout 10s -rate 15 -burst 5 -maxage 3600`.

| uji | hasil |
|---|---|
| `200` lalu `304` (`If-None-Match` dari tag yang sama) | 200 lalu 3x 304, p50 **0,39 ms** |
| `Cache-Control` | `public, max-age=3600` |
| `/metrics` | 48 baris, `route_expansions_total` terisi (414.567) |
| 9 rute penuh cepat dari satu IP | `429` + `Retry-After: 4` setelah burst habis |
| `X-Forwarded-For` | tidak dipercaya (tanpa `-trust-proxy`) |

Dibanding rute 41,7 km yang butuh ~97 ms di box ini, revalidasi 304 sekitar
**250x** lebih murah dan tidak menyentuh workspace.

**Urutan middleware yang benar (ditentukan oleh uji ini).** Percobaan pertama
memasang rate limiter sebelum pengecekan `ETag`, hasilnya 304 ikut memakai token.
Setelah limiter dipindah ke belakang pengecekan `ETag`:

| pola request (burst 3) | kode |
|---|---|
| 1 rute penuh lalu 7 revalidasi | `200 304 304 304 304 304 304 304` |
| 6 rute penuh (`If-None-Match` basi) | `200 200 200 429 429 429` |

Jadi klien yang memakai cache-nya dengan benar tidak pernah kena limit — dan
justru itu yang diinginkan: revalidasi tidak menyentuh workspace, jadi tidak
perlu "dibayar" dengan token.

Catatan operasional: dengan `-rate 15/60` sebuah app yang menghitung ulang rute
sambil menggeser peta akan kena `429` setelah burst. Naikkan `-rate`/`-burst` di
compose kalau app butuh lebih, atau kirim `If-None-Match` supaya query yang sama
tidak dihitung.

## 12. Format v4, ferry, profil motor, tangga ε (M5)

Semua angka bab ini diukur 2026-11-07 di box uji 4 core yang **dipakai
bersama** (load 3-5), jadi absolutnya tidak sebanding dengan §2-§11 di box
khusus. Yang dilaporkan adalah rasio A/B bergantian pada mesin dan graph yang
sama.

### 12a. Ukuran artefak (format v4)

`Off` uint32, `Len` uint16 (satuan 4 m), `Cls` uint8 (km/h x 2), section `Time`
dihapus; waktu tempuh dihitung saat load. Provenance v3 dan daftar ferry
(sparse) tetap ada.

| graph | v3 (MB) | v4 (MB) | delta |
|---|---|---|---|
| java | 1.019 | **693** | -32,0 % |
| sumatra | 533 | **364** | -31,7 % |
| tujuh region (nasional) | — | **1.650** | 53,9 M node / 110,2 M edge |
| java, profil motor | — | 714 | 23,4 M node / 48,2 M edge |

Waktu build (4 worker, `GOMEMLIMIT=4GiB` di box 7 GB): java 3m53s, sumatra
1m09s, nasional 8m08s.

### 12b. Biaya per query: v3 vs v4

Ekspansi **identik** (rute dan ε sama): Jakarta-Bandung 148 km = 5.586.589
ekspansi di kedua versi. Yang berubah hanya biaya per ekspansi (node record 20 ->
12 B, tanpa `Time` 4 B/edge), jadi ini murni cache/bandwidth.

CPU (user) per query, 3 ronde bergantian:

| rute | v3 | v4 | rasio |
|---|---|---|---|
| Jakarta-Bandung 148 km | 18,6-22,1 s | 11,7-13,7 s | **1,55-1,66x** |
| Jakarta-Cikarang 42 km | 2,18-2,39 s | 0,95-1,54 s | **1,6-2,5x** |

Throughput di homelab (i3-6100T, 4 core, `-conc 4`, 42,4 km
Jakarta-Cikarang, 40 request, 2 ronde, container 1,5 GiB, `-rate 0`):

| | v3 (pre-m5) | v4 | rasio |
|---|---|---|---|
| RPS | 9,97-10,58 | **13,79-16,02** | 1,4-1,5x |
| server p50 | 361-376 ms | **223-283 ms** | -30 % |
| RSS diam | 79 MiB | **64 MiB** | -19 % |

Catatan: box homelab saat ini menjalankan ~28 container lain, jadi absolutnya
jauh di bawah §10 (36-40 RPS di box yang sama ketika sepi). Rasionya yang stabil.

### 12c. Ferry dan lintas selat

| query | hasil |
|---|---|
| Ketapang -> Gilimanuk (`java.graph`) | 15,1 km / 1436 s, `ferry_m=4284`, `ferry_spans=[[321,329]]` |
| Denpasar -> Mataram (`nusa-tenggara.graph`) | 134,2 km / 17846 s, `ferry_m=68304` |
| Merak -> Bakauheni (`indonesia.graph`) | 29,2 km / 5226 s, `ferry_m=28535` |
| Jakarta -> Medan (`indonesia.graph`) | **2004,5 km / 92963 s**, `ferry_m=29072`, `relaxed=true` |

Waktu ferry diambil dari tag `duration` (jatuh ke 20 km/jam kalau tidak ada):
Merak-Bakauheni 1,45 jam, Denpasar-Mataram 19 jam termasuk lintasan Lembar.

### 12d. Tangga ε saat budget habis

Koridor >800 km melebihi `maxExpansions` (6 jt) pada ε 1,5. Sebelum menyerah,
router mencoba ε 2,0 lalu 3,0 dan menandai `relaxed=true` + `epsilon`.

Jakarta-Bandung (148,67 km pada ε 1,5):

| ε | jarak | waktu | ekspansi |
|---|---|---|---|
| 1,5 | 148,67 km | 6428 s | 5.586.589 (gagal budget) |
| 2,0 | 148,62 km (+0,07 %) | 6545 s (+1,8 %) | 103.077 |
| 2,5 | 155,30 km (+4,4 %) | — | — |

Jadi ε 2,0 praktis gratis untuk rute yang gagal, ε 3,0 hanya dipakai lintas
pulau (Jakarta -> Medan: 4,81 jt ekspansi, 19,3 s di server dengan `-timeout
60s`).

### 12e. Multi-region overlap

File Java memuat Bali, file Nusa Tenggara juga. Titik ujung bisa jadi "terdekat
ke file lain" walau satu file memuat keduanya, yang dulu berakhir `422`. Sekarang
server mengulang snap di kedua kandidat region dan menghitung
`route_cross_region_resnapped_total`. Ketapang -> Gilimanuk: `200` dari
`java.graph` (15,2 km, `ferry_m=4284`).

## 13. E1: routeid vs OSRM MLD, mesin yang sama

Berbeda dari §1-§12 (Intel i5-7360U, 2C/4T, 7 GB), bagian ini diukur di
**AMD Ryzen 5 5600G (6C/12T, 3,9-4,4 GHz), 16 GB RAM, 30 GiB swap**,
Linux 7.2.15, Go 1.27.3. Angka dari dua mesin tidak boleh dicampur.

Kedua server memakai PBF yang sama, `data/java-latest.osm.pbf` (858 MB,
diunduh 2026-11-07), dan dijalankan bersamaan di mesin ini:

| | routeid `a18cd70` | OSRM v5.26.0 (`osrm/osrm-backend:latest`, digest `af5d4a83...`) |
|---|---|---|
| perintah | `rid-server -conc 1 -rate 0 -timeout 60s` | `osrm-routed --algorithm mld` (thread default) |
| graph | format 4, 22.712.775 node / 46.676.434 edge, 3.790 ban, 593 restriction di-skip | MLD, profil `car.lua` |
Angka §13 dan §14 diukur **sebelum penalti belok ada** (biaya = waktu kelas
jalan). Default sekarang 0,25x penalti belok; dampak terukurnya di §15, dan
p95 jarak tidak berubah (21,12%).

### 13a. Preprocess dan artefak

| tahap | wall | peak RSS |
|---|---|---|
| `rid-extract` (GOMEMLIMIT=2GiB) | 28 s | tidak diukur di run ini |
| `osrm-extract` | 151 s | 6.254 MB |
| `osrm-partition` | 83 s | 3.798 MB |
| `osrm-customize` | 16 s | 2.350 MB |
| total OSRM | 250 s | 6.254 MB |

Artefak: `java.graph` **693.111.912 B** (693 MB / 661 MiB) vs
`java-latest.osrm*` **5.082.571.256 B** (5,08 GB / 4,73 GiB), rasio **7,3x**.
Peak RSS OSRM diukur dengan `docker stats` per 2 s; tahap <2 s bisa terbaca
`n/a`.

### 13b. Beban dan filter pasangan

`paper/results/pairs-java.csv` (600 pasang, 120/bucket). Generator pasangan
memilih node sumber di jalan lalu menggeser titik tujuan sejauh bucket dengan
bearing acak; akibatnya banyak titik tujuan jatuh di laut/hutan. Karena itu
perbandingan jarak hanya memakai pasangan yang valid untuk kedua engine:

- snap routeid dan OSRM di kedua ujung <= 250 m, dan
- titik snap kedua engine berjarak <= 50 m di kedua ujung (bukan hanya dekat,
  tetapi menempel ke ruas yang sama).

Hasil: **332 pasang cocok**, **81 routeid 422**, **187 keluar filter snap**.
Tanpa filter ini, selisih jarak didominasi perbedaan titik snap (OSRM dapat
menempel puluhan kilometer dari input), bukan perbedaan rute.

### 13c. Latency (HTTP, bergantian)

Urutan request bergantian (OSRM dulu di pasangan genap, routeid dulu di ganjil),
satu per satu. `p95|Δ|` = persentil 95 selisih jarak absolut dari 332 pasang.

| bucket | cocok | 422 | keluar | ride p50 | ride p95 | OSRM p50 | OSRM p95 | Δ p50 | p95\|Δ\| | max\|Δ\| | >10% |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 0-5 km | 86 | 0 | 34 | 3,6 ms | 14,0 ms | 0,7 ms | 1,1 ms | +0,030% | 21,05% | 45,17% | 10 |
| 5-15 km | 73 | 6 | 41 | 19,7 ms | 68,7 ms | 1,1 ms | 1,7 ms | −0,030% | 14,96% | 48,30% | 5 |
| 15-30 km | 67 | 12 | 41 | 54,7 ms | 226,7 ms | 1,7 ms | 2,3 ms | −0,00% | 22,73% | 45,04% | 10 |
| 30-50 km | 56 | 27 | 37 | 139,0 ms | 478,4 ms | 2,2 ms | 3,0 ms | −0,020% | 14,68% | 29,82% | 5 |
| 50-80 km | 50 | 36 | 34 | 397,3 ms | 1.060,9 ms | 3,2 ms | 4,5 ms | +0,01% | 22,03% | 23,81% | 8 |

Total 332 pasang: latency p50 **35,1 ms vs 1,4 ms** (~25x), p95 **522 ms vs
3,4 ms**. Jarak: median **−0,005%**, p95 **21,05%**, max **48,30%**,
38/332 (11,4%) >10% dan 10/332 >25%.

Gerbang yang ditetapkan di `COMPARISON.md` (median <5%, p95 <12%) **lulus di
median, gagal di p95**. Selisih besar terkonsentrasi di minoritas pasangan.
Audit §14 menunjukkan penyebabnya **perbedaan fungsi biaya**, bukan
restriction: routeid meminimalkan waktu murni (termasuk `track`), OSRM
meminimalkan `weight` (durasi + penalti belok + preferensi kelas jalan).
Tidak ada klaim akurasi sampai E2.

Durasi tidak sebanding (routeid memakai tabel kecepatan kelas jalan, OSRM
`car.lua` memakai maxspeed); rasio durasi r/o 0,93-1,02 hanya dicatat, bukan
klaim.

### 13d. Kegagalan dan biaya memori

Dari 81 routeid 422: 72 `no road within snap distance` (radius server 3 km),
7 `search space exhausted`, 2 `no route`. OSRM tidak gagal di satu pun, tetapi
pada 81 pasangan itu titik snap tujuan OSRM berjarak **median 14,7 km, p90
32,7 km, max 44,6 km** dari koordinat input — rute OSRM valid secara internal
tetapi bukan pembanding yang setara. Routeid menolak titik seperti itu;
OSRM menempel sejauh apa pun.

| metrik | routeid | OSRM |
|---|---|---|
| RSS setelah start (Java, sebelum query) | 17.280 kB | 2,362 GiB |
| RSS setelah 600 pasangan | 815.312 kB | 2,451 GiB |
| artefak | 693.111.912 B | 5.082.571.256 B |

Angka OSRM dari `docker stats --no-stream`; keduanya memakai mmap sehingga RSS
ikut menghitung halaman file yang tersentuh, bukan hanya heap. RSS routeid
setelah start diukur dari proses terpisah (`-addr :8081`), lalu dimatikan.

Instrumentasi 2026-11-08 (server 3 region, `-conc 4`, `GOMEMLIMIT=1GiB`; rute
Jakarta-Bandung 148 km dan Banda Aceh-Medan 579 km) menunjukkan komposisi RSS
routeid: cgroup `anon` 612 MB + `file` 45 MB + kernel 3 MB = 629 MiB, sementara
halaman graf yang resident hanya ~45 MB. Sumber `anon` adalah tabel workspace
(`keys`/`gs`/`came`, 12 B/slot): tabel dobel saat load 80% dan tidak mengecil
sampai restart, jadi satu slot menahan 4 M slot = 48 MB setelah rute 148 km
(3,16 jt ekspansi) atau 579 km (1,93 jt ekspansi). Frontier heap A* maksimum
hanya 25.621 item (~0,2 MB), jadi frontier bukan penyebabnya. `NewRouter`
dipanggil per region, sehingga batas memori = jumlah region x `-conc` x tabel
terbesar; `maxSlots` 1<<25 berarti 384 MiB/slot pada kasus terburuk. Efek
`GOMEMLIMIT` kecil: 1 GiB -> `anon` 516 MB, 512 MiB -> 459 MB.

### 13e. Reproduksi

```bash
# routeid
GOMEMLIMIT=2GiB bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java.graph
bin/rid-server -graph data/java.graph -addr 127.0.0.1:8080 -conc 1 -rate 0 -timeout 60s

# OSRM (Docker; butuh ~7 GB saat extract)
sudo -u arifsani -g docker ./scripts/m6-osrm.sh extract data/java-latest.osm.pbf
sudo -u arifsani -g docker docker run -d --name osrm-routed -p 5000:5000 \
  -v "$PWD/data:/data" osrm/osrm-backend:latest \
  osrm-routed --algorithm mld /data/java-latest.osrm

scripts/m6-compare.py --pairs paper/results/pairs-java.csv \
  --routeid http://127.0.0.1:8080 --osrm http://127.0.0.1:5000 \
  --max-snap 250 --max-gap 50 --csv paper/results/m6-osrm-vs-routeid.csv
```

CSV memuat snap mentah kedua engine (`ride_snap_*`, `osrm_snap_*`,
`snap_gap_*`) supaya filter bisa diaudit ulang. Latency antar-run bergeser
~5% (contoh p50 0-5 km: 3,6-3,9 ms); angka di tabel berasal dari run terakhir
yang menghasilkan CSV di repo.
## 14. Audit p95 jarak E1: restriction bukan penyebabnya
§13c menyisakan dua kandidat penyebab p95 jarak 21,05%: 596 restriction yang
di-skip saat build dan perbedaan profil akses. Bagian ini menguji keduanya.
### 14a. Komposisi 596 restriction yang di-skip
Instrumentasi sementara (`RID_AUDIT_RESTRICTIONS=1`, sudah dihapus) mencetak
setiap relation yang gagal dipetakan ke pasangan edge terlarang, lalu
diklasifikasikan terhadap tag dan membernya:
| sebab | jumlah | isi | status |
|---|---|---|---|
| `not_car` | 236 | from/to way di luar graf profil (footway, akses terlarang) | benar di-skip |
| `bad_type` | 184 | 165 hanya `restriction:conditional`; 19 khusus kelas (`motorcar` 7, `hgv` 8, `bus`/`bicycle`/`psv`/`truck` 4) | 165 = batasan model; **7 `restriction:motorcar` = bug** |
| `missing` | 62 | relation `type=restriction` tanpa member via | data rusak, benar di-skip |
| `unresolved` | 67 | `no_out` 38, `no_in` 11, `not_on_way` 4, `no_node` 1 | mayoritas maneuver mustahil (via hanya bisa dimasuki/dilewati satu arah) |
| `chain` | 29 | via-way: `only_*` sengaja di-skip + rantai via-way gagal diurutkan | batasan representasi |
| `except` | 16 | `except=motorcar`/`motor_vehicle`/`vehicle` | benar untuk profil car |
| `conditional` | 2 | `restriction` + `restriction:conditional` | time-dependent, belum dimodelkan |
Tidak ada satu pun dari 596 yang, setelah diperiksa geometrinya, merupakan
larangan mobil yang dilanggar routeid. `no_out`/`no_in` muncul karena `to`
atau `from` way berarah menuju/menjauhi via: maneuver yang dilarang memang
tidak mungkin dilakukan.
Bug `restriction:motorcar` diperbaiki: pemilihan tag kini mengikuti profil
(car: `restriction:motorcar` -> `restriction:motor_vehicle` ->
`restriction`; motor: `restriction:motorcycle` -> `restriction:motor_vehicle`
-> `restriction`), dan `except` dievaluasi per profil. Hasil rebuild:
`usable` 4.198 -> 4.202, `ban` 3.904 -> 3.907, `only` 154 tetap, `skipped`
596 -> **593** (`not_class` 14, `conditional` 167, `bad_type` 1), healthz
3.787 -> **3.790 ban / 593 skipped**. Sisa `bad_type` 1 = relation tanpa tag
`restriction` apa pun. Rebuild ini tidak mengubah satu pun hasil E1: 600/600
jarak dan durasi identik dengan CSV run sebelumnya.
### 14b. Metode audit rute
Untuk 38 pasangan dengan |Δjarak| > 10%:
1. Geometri rute routeid di-`/match` ke OSRM (`radiuses=50`, `gaps=ignore`)
   untuk mengukur bagian geometri yang berada di jaringan mobil OSRM.
2. Rute OSRM dipecah tiap ~2 km, lalu tiap segmen ditanyakan ke routeid;
   jumlah durasinya = biaya path OSRM menurut model routeid.
3. Kasus terburuk diperiksa manual di PBF (`/nearest` OSRM + way di sekitar
   titik divergensi).
### 14c. Temuan
| temuan | angka |
|---|---|
| coverage `/match` 38 outlier | median **0,954**; 10 < 0,90; 5 < 0,80 |
| kasus terburuk −48,30% | routeid 20,6 km vs OSRM 39,9 km; divergensi di way `644225981` `highway=track` `surface=unpaved`; `/nearest` OSRM berjarak 1.512 m; `car.lua` hanya memuat motorway..service (tanpa `track`) |
| kasus −45,17% | tidak ada restriction dalam radius 400 m; path routeid seluruhnya residential/living_street/tertiary; `/match` path routeid 1,6 km / 189-203 s vs rute OSRM 229 s |
| biaya path OSRM di model routeid (3 outlier positif terbesar) | 3.582 s vs rute routeid 3.537 s; 2.456 s vs 2.378 s; 4.730 s vs 4.648 s |
| artinya | rute routeid 1-3% lebih cepat **di modelnya sendiri**, dengan jarak 15-30% lebih panjang |
| `-eps 1.0` pada kasus terburuk | jarak identik 68.727 m -> bukan suboptimalitas heuristik |
Kesimpulan: p95 jarak tidak disebabkan restriction maupun bug pencarian.
Routeid meminimalkan **waktu tempuh murni** (tanpa biaya belok, tanpa
preferensi kelas jalan, `track` ikut); OSRM meminimalkan `weight` =
durasi + penalti belok + preferensi kelas jalan. Jarak adalah besaran ketiga
yang tidak dioptimalkan oleh keduanya, sehingga selisih jarak mengukur
perbedaan fungsi biaya, bukan akurasi.
### 14d. Implikasi
- Gerbang "p95 selisih jarak < 12%" di `COMPARISON.md` **tidak sah sebagai
  gerbang akurasi**; ia hanya mengukur kemiripan rute. Statusnya tetap gagal,
  tetapi tidak boleh dipakai untuk menyimpulkan routeid salah.
- Klaim akurasi tetap menunggu E2 (Google) — hanya referensi independen yang
  bisa memutuskan rute mana yang lebih baik.
- Untuk menurunkan selisih jarak: tambahkan penalti belok dan/atau preferensi
  kelas jalan (mengubah perilaku routing, harus diikuti E1 ulang), atau ubah
  metrik primer E1 menjadi durasi. Penalti belok sudah diimplementasikan dan
  diukur, hasilnya di §15; preferensi kelas jalan belum.
- 167 `restriction:conditional` tetap batasan model (butuh evaluasi waktu),
  dicatat di `BACKLOG.md`.

## 15. Penalti belok: implementasi dan dampaknya pada E1
Dasar: §14 menyimpulkan selisih jarak berasal dari fungsi biaya. Penalti belok
dicoba lebih dulu karena ia satu-satunya bagian `weight` OSRM yang bisa
ditambahkan tanpa mengubah tabel kecepatan maupun format graf.

### 15a. Model biaya
Biaya per edge = waktu kelas jalan (tidak berubah) + biaya belok masuk edge.
Biaya belok dihitung dari sudut antara arah datang dan arah keluar pada node
(bearing meter, `dirTo`), dibucket seperti tabel di bawah; total biaya belok
dikalikan `-turn-penalty` (default **0,25**, `0` mematikan).
| sudut | kosinus | biaya tabel | pada 0,25x |
|---|---|---|---|
| <= 22,5° (lurus) | >= 0,92388 | 0 | 0 |
| 22,5-67,5° | >= 0,38268 | 2 s | 0,5 s |
| 67,5-135° | >= -0,70711 | 7,5 s | 1,875 s |
| > 135° (U-turn) | < -0,70711 | 20 s | 5 s |
Biaya belok masuk ke `TimeDs` yang dilaporkan, jadi ETA ikut naik; jarak dan
`EdgeTimeDs` per edge tidak berubah. Tidak ada perubahan format graf
(`format:4`, fingerprint `e6148c94f791`); `ProfileHash` hanya provenance,
bukan validasi, jadi graf E1 yang ada tetap dipakai.
### 15b. Metode
Graf, pasangan, filter snap, dan container OSRM sama dengan §13. Empat run
harness berturut-turut pada mesin yang sama, hanya `-turn-penalty` yang
berbeda: 0 (CSV §13), 0,25, 1, 2. Jarak bersifat deterministik sehingga bebas
dari noise beban; **latency antar-run tidak sebanding** (run 0,25x dan 1x
berjalan saat mesin dibebani proses lain: p50 0-5 km 4,5 ms dan 12,8 ms vs
3,6 ms di run §13), jadi latency tidak dilaporkan ulang di sini.
### 15c. Hasil (332 pasang, |Δ| jarak relatif terhadap OSRM)
| konfigurasi | rute berubah | mean \|Δ\| | p50 | p75 | p90 | **p95** | p99 | max | >10% | ETA vs 0 |
|---|---|---|---|---|---|---|---|---|---|---|
| 0 (sebelum §15) | - | 3,60% | 0,46% | 3,03% | 11,20% | **21,12%** | 43,65% | 48,30% | 38 | - |
| 0,25x (dipakai) | 111/600 | 3,50% | 0,39% | 2,82% | 10,56% | **21,12%** | 43,79% | 48,28% | 37 | +1,6% (p95 +5,1%) |
| 1x | 227/600 | 3,96% | 0,47% | 3,65% | 12,52% | **22,02%** | 40,97% | 45,17% | 43 | +6,1% (p95 +19,7%) |
| 2x | 294/600 | 4,19% | 0,59% | 4,36% | 13,19% | **22,02%** | 43,79% | 47,62% | 44 | +11,7% (p95 +37,2%) |
Bacaan:
- Penalti belok **tidak menurunkan p95**: 21,12% di semua skala <= 0,25x dan
  naik ke 22,02% pada 1x. Ekor distribusi tidak ditentukan oleh jumlah belok.
- 1x memperbaiki outlier ekstrem (−48,30% -> −17,03%; p99 43,65% -> 40,97%)
  tetapi merusak banyak pasangan menengah, sehingga mean dan p75 memburuk.
  Contoh 0-5 km: 3.786 m/530,5 s menjadi 5.314 m/582,2 s — rute optimum di
  model baru memang 54 s waktu tempuh lebih mahal, ditebus penalti belok.
- 0,25x menggeser seluruh badan distribusi sedikit ke arah OSRM (mean −0,10
  poin, p75 −0,21, p90 −0,64) dengan 111 rute berubah dan ETA +1,6% median.
  Ini alasan default dipilih 0,25x, bukan 1x.
- Uji heuristik: dengan 1x, `-eps 1.5` dan `-eps 1.2` menghasilkan rute
  **identik pada 600/600 pasangan**; satu pasang 30-50 km pada `-eps 1.0`
  juga memberi rute yang sama (jarak 106.760 m, waktu 5.587,2 s; ekspansi
  1,28 juta / 1,53 juta / 1,73 juta). Perubahan rute berasal dari model
  biaya, bukan suboptimalitas weighted A*.
### 15d. Kesimpulan
- Ekor p95 tetap digerakkan perbedaan **tabel kecepatan kelas jalan**
  (routeid 1-3% lebih cepat di modelnya sendiri, jarak 15-30% lebih panjang),
  bukan jumlah belok. Menutup gerbang p95 < 12% butuh kalibrasi kecepatan
  kelas (atau metrik primer diganti durasi), dicatat di `BACKLOG.md` 16c.
- Penalti belok tetap dikirim karena: menghilangkan detour ekstrem yang
  membuat ETA tidak masuk akal (p99/max membaik pada 1x), memberi tie-break
  terukur pada 0,25x, dan menyiapkan permukaan untuk preferensi kelas jalan.
- Golden test M1 diregenerasi: waktu rute sintetis naik ~1% (0,25x), jarak
  mayoritas identik; beberapa pasangan snap berubah node/total karena rute
  memilih edge snap yang berbeda.

## 16. Kapasitas pada batas desain 40 km (i3-6100T, build terbaru)

§12b mengukur satu rute contoh 42 km; bagian ini mengukur **himpunan pasangan**
yang jaraknya <= 40 km dengan build yang sekarang dikirim (penalti belok 0,25x,
3 region termuat), karena aplikasi membatasi rute maksimum 40 km.

### 16a. Metode
- Host homelab: Intel i3-6100T (2 core/4 thread), 7,8 GB, 19 container lain
  tetap jalan; graf Java (`5d3fdd1b`, ban 3.790, skip 593), Sumatra
  (`58ebc2b0`, 511/48), Nusa-Tenggara (`bdde65eb`, 181/42).
- Instance uji terpisah di port 18081: `GOMEMLIMIT=768MiB -conc N -rate 0
  -timeout 120s` (sama dengan container, kecuali rate limit dimatikan).
- 600 pasang `paper/results/pairs-java.csv` dikirim berurutan (HTTP keep-alive);
  298 menghasilkan rute <= 40 km (jarak p50 12,6 km, p95 36,4 km); 81 tetap
  `422` karena snap > 3 km, bukan karena panjang rute.
- Throughput: subset <= 40 km yang sama, klien keep-alive, 20 s per level.

### 16b. Hasil (298 pasang <= 40 km)

| concurrency | RPS | p50 | p95 | p99 | max |
|---|---|---|---|---|---|
| 1 | — | **24,3 ms** | **246,2 ms** | 419,9 ms | 609,5 ms |
| 2 | 24,66 | 32,9 ms | 336,6 ms | 577,9 ms | 780,4 ms |
| 4 | 23,50 | 73,6 ms | 705,4 ms | 982,0 ms | 1.145,2 ms |
| 8 | 24,85 | 192,7 ms | 1.205,0 ms | 1.531,3 ms | 1.810,5 ms |

- Plafon CPU 2C/4T ~**24-25 RPS**; level 2/4/8 jenuh pada angka yang sama.
- `-conc 2` memberi p50 sepertiga `-conc 4` (32,9 vs 73,6 ms) pada RPS sama,
  jadi untuk batas 40 km `-conc 2` cukup dan antrean lebih pendek.
- RSS 805 MB steady, puncak 859 MB (limit heap 768 MiB) dengan tiga region.
- Seluruh 600 pasang (tanpa batas): p50 92,6 ms, p95 1.550,7 ms — ekor itu dari
  pasangan panjang dan `422`, bukan dari rute <= 40 km.

### 16c. Akurasi pada segmen <= 40 km (E1, 324 pasang OSRM <= 40 km)
Median selisih jarak **0,03%**, p95 **7,44%**, max 70,12%: gerbang p95 < 12%
**lulus** pada segmen ini. Ekor 21,12% di §13/§15 berasal dari pasangan panjang;
jadi batas 40 km juga memperbaiki sisi akurasi, bukan hanya kapasitas.

### 16d. Reproduksi
```bash
GOMEMLIMIT=768MiB bin/rid-server -graph data/java.graph -graph data/sumatra.graph \
  -graph data/nusa-tenggara.graph -addr 127.0.0.1:18081 -conc 1 -rate 0 -timeout 120s
# 600 pasang dari paper/results/pairs-java.csv via HTTP keep-alive; saring distance_m <= 40000
```

Catatan: angka latency E1 (§13c) untuk segmen ini tidak dipakai — sebagian
baris 0-5 km/30-50 km tercatat 1-4 ms (terkontaminasi cache/alat ukur), jadi
tabel di atas adalah pengukuran ulang bersih di mesin yang sama dengan deploy.
