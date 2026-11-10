# Backlog teknis

Rincian item dari [`ROADMAP.md`](ROADMAP.md). Urutan di sini mengikuti prioritas
milestone, bukan abjad. Format tiap item: masalah -> bukti -> rencana -> kriteria
lulus -> biaya.

Ukuran biaya: **S** = < setengah hari, **M** = 1-2 hari, **L** = 3-5 hari.

---

## M1. Kepercayaan

### 1. Metadata di header graph (S), selesai (M1)

**Masalah.** Header 256 byte sudah menyimpan `magic` (`RIDG`) dan `version`
(sekarang 2), plus `VersionMin` untuk kompatibilitas, tetapi **tidak menyimpan
provenance**: tidak ada `pbf` SHA-256, tanggal build, atau hash tabel kecepatan.
Artefak 972 MB karena itu tidak bisa diaudit: tidak tahu dari ekstrak OSM tanggal
berapa dan dengan profil versi mana. Ini celah E6 (reproduktibilitas) di paper.

**Rencana.** Isi header:
`magic|version|nodeCount|edgeCount|turnCount|coordCount|pbfSHA256[32]|buildUnix|
profileHash|extractorVersion|flags`. Flag wajib naik ke `v2`; `rid-server`
menolak format lama dengan pesan jelas. `rid-extract` menghitung SHA-256 dari
pbf saat membaca (streaming, tanpa buffer penuh).

**Lulus bila.** `rid-graphinfo` dan `/healthz` menampilkan pbf hash + tanggal
build + profileHash; graph v1 lama ditolak dengan pesan yang menyebut cara
rebuild.

**Hasil (M1).** Format v4 menyimpan `PBFHash`, `BuildUnix`, dan metadata
`extractor`, `profile`, `profile_sha256`, `source`, `pbf_bytes`, `order`, serta
`grid_step`. `rid-graphinfo`, `/healthz`, dan `Provenance()` menampilkannya;
format < 4 ditolak dengan pesan versi yang didukung.

### 2. Build info di binary (S), selesai (M1)

**Masalah.** Tidak ada cara mengetahui binary mana yang sedang jalan di homelab
(rsync menimpa `bin/static`).

**Rencana.** `-ldflags "-X main.commit=... -X main.buildTime=..."` di `Makefile`,
tampilkan di `/healthz` dan `/version`.

**Lulus bila.** `/healthz` menyebut commit 7 karakter yang ada di `git log`.

**Hasil (M1).** `make build` menyuntikkan `main.commit` dan `main.buildTime`
lewat `-ldflags`. `/version` memuat commit, waktu build, versi Go, OS, dan
arsitektur; `/healthz` memuat `version` dan `commit`.

### 3. Golden test regresi rute (S), selesai (M1)

**Masalah.** Setiap perubahan (snap, ALT, urutan) bisa menggeser rute tanpa
ketahuan. Sekarang tidak ada test yang menangkap itu.

**Rencana.** `testdata/golden-pairs.csv` (50 pasangan, berbagai kelas) + file
harapan berisi `distance_m`, `points_n`, dan hash FNV polyline. Test membaca
graph kecil (`data/test.graph` yang di-commit, ~2 MB) supaya CI tidak butuh pbf.

**Lulus bila.** Mengubah bobot atau snap tanpa sengaja membuat test gagal dengan
diff per pasangan.

**Hasil (M1).** `internal/graph/golden_test.go` memakai graph uji kecil dan
`testdata/golden.txt` berisi 48 pasangan node + 48 pasangan snap (trim/spur dan
offset ikut dipin), plus `testdata/motor-golden.txt`. Perubahan bobot atau snap
yang tidak disengaja menggagalkan test dan menampilkan diff per pasangan.

---

## M2. Akurasi ujung rute

### 4. Snap ke ruas (SELESAI, M)

**Masalah.** `internal/graph/snap.go` memilih node terdekat. Di jalan yang
way-nya jarang titik, titik user jatuh ke node 100-300 m jauhnya -> awal/akhir
rute terlihat "mundur".

**Rencana.**
1. Tambah seksi grid-edge: untuk setiap sel, daftar edge yang menyentuh sel.
2. Kandidat terbaik = proyeksi tegak lurus ke segmen (a,b), jarak titik-ke-ruas,
   plus parameter t.
3. Ujung rute: gabungkan rute A* dengan dua bagian parsial: separator dari
   proyeksi ke node awal edge, dan dari node akhir edge ke proyeksi tujuan.
   Jangan membuat pseudo-node (menghindari mutasi graph); sisipkan geometri di
   layer respons.
4. Batas: hanya edge yang boleh dilalui profil kendaraan.

**Hasil.** `SnapEdge` + `RouteSnap` di `internal/graph/snap.go` dan
`route_snap.go`, dipakai `/route` dan `rid-route`. Golden test sekarang memuat 48
pasangan node **dan** 48 pasangan snap (trim/spur + offset ikut dipin).

Terukur (`RESULTS.md` §9): bucket ruas >150 m, galat 67,3 → 16,1 m (p95 114,0 →
34,9 m). Implementasi dibanding oracle: mean −1,25 m, 0% sampel lebih buruk >1 m.
Tidak ada perubahan format file.

**Sisa keterbatasan.** Kalau rute tidak keluar lewat ruas yang di-snap (mis. jalan
satu arah), offset ditambahkan sebagai spur lurus, masih garis menyeberang
bangunan sepanjang `Along`. Menghilangkan ini butuh pseudo-node (opsi 2).

**Sisa keterbatasan kedua (SELESAI sebagian, terukur 2026-11-08).** `SnapEdge`
memilih ruas terdekat tanpa melihat keterhubungan, jadi titik yang jatuh di ujung
buntu bisa menghasilkan `422 no route` padahal ada jalan lain beberapa ratus
meter lagi. Di graf Jawa: 3606 komponen / 2290 node tanpa ruas keluar / komponen
terbesar 99,60% node (mobil: 5170 / 2312 / 99,56%). Contoh: Monas (-6.1754,106.8272)
di graf motor. Node hasil snap `16405111` hanya menjangkau 3 node ke depan
(seluruh komponen lemahnya 62 node, tapi sisanya baru tercapai lewat ruas yang
mengarah masuk), dan node di luar pocket terdekat ada di 338 m.

**Perbaikan.** `SnapEdges` mengembalikan k kandidat terurut jarak (satu per node,
kandidat lebih dekat menggantikan yang lebih jauh), `ForwardSize(node, 512)`
mengukur keterjangkauan maju, dan handler mencoba 4 pasangan kandidat saat A*
mengembalikan `ErrNoRoute`. Kandidat awal yang hanya menjangkau <= 512 node
dilewati; kalau seluruh 32 kandidat terdekat begitu (pocket lebih lebar dari
daftar kandidat), scan diulang sekali dengan 512 kandidat. Kalau tetap kosong,
urutan jarak murni dipakai supaya jaringan kecil yang sah (pulau) tetap dicoba.
Percobaan ulang hanya berjalan setelah percobaan pertama gagal, jadi jalur
normal tidak berubah, dan keberhasilannya dihitung di `route_snap_retry_total`.

**Hasil (200 pasangan acak bbox Jakarta, seed 42, kedua profil).**

| profil | sebelum | sesudah |
|---|---|---|
| motor | 190 ok / 6 `no_route` / 4 anggaran habis | 196 / 0 / 4 |
| mobil | 184 ok / 10 `no_route` / 6 anggaran habis | 193 / 0 / 7 |

Monas di `/route` motor: `422` → `200` (3806 m, snap 338 m). Satu pasangan mobil
yang sebelumnya `no_route` sekarang berakhir di `too_far` (46,4 km, melewati
`-max-route-km` 40 km), jadi `422` anggaran ekspansi tetap 4-7 dan itu batasan
anggaran, bukan snap. Diulang pada image `81722ea` di homelab dengan pasangan
yang sama: motor 196 ok / 0 `no_route` / 4 (habis `-timeout` 10 s), mobil
193 / 0 / 7, `route_snap_retry_total` 6 dan 10.

**Batasan sisa.** Pemeriksaan hanya di sisi awal: graf hanya punya CSR maju, jadi
"ada yang bisa mencapai tujuan" butuh indeks masuk terbalik (memori atau format
baru). Pin tujuan di dalam pocket karena itu masih bisa `422 no_route`.

### 5. Ukur galat snap terhadap oracle (SELESAI, S)

**Masalah.** Belum ada angka galat ujung sama sekali.

**Hasil.** `rid-snapcheck` sekarang melaporkan node vs ruas vs oracle, dengan
bucket panjang ruas, dan `-bbox` supaya bisa mengukur satu kota tanpa memuat
seluruh graf ke memori. Angkanya di `paper/RESULTS.md` §9.

---

## M3. Densitas

### 6. ALT landmark (L), ditunda (M3)

**Masalah.** Saturasi 4 query = 3,6 core-ekuivalen pada A* dengan eps 1.5. Cara
menaikkan RPS tanpa core tambahan hanya mengurangi ekspansi.

**Rencana.**
1. `rid-extract -landmarks 16`: pilih landmark deterministik (farthest-point
   sampling dari grid 4x4), jalankan Dijkstra dari/tujuan tiap landmark saat build.
2. Simpan `DistLandmark[16][node]` (uint32; Infinity = tak terjangkau) + untuk
   graf berarah juga jarak ke landmark (arah balik).
3. Heuristik: `max_l max(|d(l,v) - d(l,t)|, |d(v,l) - d(t,l)|)`, di-scale eps.
   Tetap admissible karena Dijkstra penuh.
4. Ukur: ekspansi, latency, RSS tambahan (16 x 4 B x 22,7 jt = 145 MB untuk Java,
   **terlalu besar**; alternatif: 8 landmark uint32 = 72 MB, atau simpan
   `uint24`, atau landmark hanya untuk node di jalan kelas >= tertiary).

**Status M3: ditunda, dengan angka.** Setelah M3 menurunkan biaya per ekspansi
1,9-2,0x (lihat RESULTS §10), ALT harus dinilai ulang: keuntungannya sekarang
hanya mengurangi ekspansi, bukan lagi menghilangkan 39% CPU trigonometri. Biaya
terukur: `d(l,·)` sebagai `uint16` (skala 1/8 detik, saturasi admissible karena
`min(d,cap)` tetap batas bawah) = 4 landmark x 2 B x 22,7 jt = **182 MB Java**,
2 landmark = 91 MB. Rencana sidecar: file `map.alt` terpisah (magic, jumlah
landmark, id landmark, array uint16), dibuka hanya kalau flag `-alt map.alt`
diberikan, sehingga budget "ringan" tetap berlaku untuk deploy default.

**Catatan penting.** Karena biaya memorinya besar, ALT harus diuji terhadap
budget: kalau +91 MB menaikkan RPS 2x, densitas tetap naik, jadi masih layak;
kalau hanya 1,2x, tolak dan pilih opsi lain.

**Lulus bila.** >= 20 RPS @42 km di i3-6100T, RSS beban <= 350 MB, jarak rute
identik (golden).

### 7. Bidirectional A* (M), ditunda (M3)

**Masalah.** Frontier satu arah besar; tiap node yang di-pop menambah kerja.

**Rencana.** CSR balik (`Roff`/`Rto`), hentikan saat kedua frontier bertemu,
heuristik tetap admissible (`(d(v,t)-d(s,v))/2`).

**Status M3: ditunda, biaya memori lebih besar dari perkiraan awal.** CSR balik
butuh `Rto` uint32 per edge (24 jt x 4 B = 96 MB Sumatra, 187 MB Java) plus
`Roff` per node (11,9 jt x 4 B = 48 MB Sumatra, 91 MB Java), total **144 MB
Sumatra / 278 MB Java**, di atas batas RSS beban 350 MB sebelum workspace.
Dua arah juga menggandakan frontier, jadi penghematan workspace hanya ~25%.
Opsi berikutnya kalau tetap dikerjakan: reverse CSR sebagai sidecar opt-in
sama seperti ALT, dibangun `rid-extract -reverse` setelah graph utama.

**Lulus bila.** Ekspansi turun >= 30% untuk rute 15-42 km tanpa perubahan jarak.

### 8. Prealokasi workspace A* (S), selesai sebagian (M3)

**Masalah.** Puncak heap 876 MB setelah 29 rute panjang karena array tumbuh
(grow menyalin). Ini juga sumber GC spike.

**Status M3: selesai sebagian, tanpa prealokasi.** Yang dikerjakan: kunci dan
generasi workspace berbagi satu `uint32` sehingga 16 B/slot -> **12 B/slot
(−25%)** dan satu array lebih sedikit untuk di-probe; load factor tabel dinaikkan
0,7 -> 0,8 sehingga tabel tumbuh lebih lambat. Prealokasi dari perkiraan jarak
tidak dikerjakan karena grow yang ada hanya terjadi sekali per slot pool lalu
berhenti; yang lebih menentukan puncak adalah ukuran tabel, bukan penyalinan.

**Lulus bila.** Rute 148 km memakai <= 60% heap sekarang; tidak ada regresi
latency > 5%. (Butuh pengukuran ulang puncak heap dengan graph 148 km; angka
876 MB berasal dari graph sebelum v3.)

---

## M4. Produksi

### 9. Timeout & pembatalan (S), selesai (M4)

**Masalah.** `cmd/rid-server/main.go` tidak punya timeout per request; query
579 km menahan slot ~2 s, dan klien bisa menumpuk sampai memori naik.

**Rencana.** `http.TimeoutHandler` 10 s + `context.WithTimeout` diteruskan ke
`Router`, dengan cek `ctx.Err()` di loop A* setiap 4096 pop. Setelah timeout,
kembalikan `504` berisi `{"error":"deadline exceeded"}`.

**Lulus bila.** Query yang dipaksa lambat berhenti <= 10 s dan slot langsung
tersedia untuk request berikutnya.

**Hasil (M4).** Server memakai `context.WithTimeout` per request, cek
`ctx.Err()` di loop A*, dan membalas `504 {"error":"deadline exceeded"}`.
Default 10 s lewat `-timeout`; test handler menutup kontrak ini.

### 10. Rate limit & bind (S), selesai sebagian (M4)

**Rencana.** Pembatas token-bucket per IP (15/menit, burst 5) sebagai middleware;
`-bind 127.0.0.1` default di `deploy/docker-compose.yml` + Caddy `reverse_proxy`.

**Lulus bila.** `ab`/loop 100 request dari satu IP menghasilkan `429` setelah
jatah, tanpa memengaruhi IP lain.

**Hasil (M4).** Token bucket per IP (default 15/menit, burst 5) aktif, dengan
`-trust-proxy` untuk `X-Forwarded-For`; balasan `429` memuat `Retry-After`.
**Sisa:** bind `127.0.0.1` + Caddy belum dipasang karena homelab tidak memakai
reverse proxy dan bind loopback akan memutus akses LAN/Postman.

### 11. `/metrics` (S), selesai (M4)

**Rencana.** Counter: `route_requests_total{format,status}`,
`route_duration_seconds` (histogram), `astar_expansions_sum`,
`restriction_fallback_total`, `region_select_total{region}`.

**Lulus bila.** `curl /metrics` menampilkan angka yang cocok dengan 1 request
yang dilakukan manual.

**Hasil (M4).** `/metrics` tanpa dependensi mengekspos
`route_requests_total{code}`, histogram `route_request_duration_seconds`,
`route_inflight`, `route_expansions_total`, `route_fallback_total`,
`route_relaxed_total`, `route_rate_limited_total`, `route_canceled_total`,
`route_errors_total{reason}`, `route_cross_region_*`, `route_snap_retry_total`,
dan `routeid_build_info`.

### 12. ETag & cache (S), selesai (M4)

**Rencana.** Hash dari `(from,to,format,simplify,version graph)`; balas `304`
bila `If-None-Match` cocok; `Cache-Control: public, max-age=60`.

**Lulus bila.** Request kedua identik balas `304` tanpa menjalankan A*.

**Hasil (M4).** ETag = hash fingerprint graph (`pbf_sha256`, node/edge, commit)
digabung query ternormalisasi dan format/simplify; `If-None-Match` dibalas `304` sebelum
limiter dan sebelum A*. Respons sukses memuat `Cache-Control: public,
max-age=3600` (default `-maxage`) dan `Vary: Accept-Encoding`.

---

## M5. Cakupan

### 13. Profil motor (M), selesai (M5)

**Hasil.** Dua artefak, bukan satu graph dengan bobot ganda: `-profile car|motor`
saat build. Alasannya diukur: kelas kecepatan motor menambah 2,3 M node / 1,5 M
edge (jalan/gang yang boleh motor) sehingga `data/java-motor.graph` = 714 MB vs
693 MB, lebih murah daripada menyimpan dua tabel waktu per edge di runtime.

| rute | mobil | motor | catatan |
|---|---|---|---|
| Jakarta-Cikarang 42 km | 42,4 km / 1941 s | 40,8 km / 2525 s | motor menghindari tol, waktu 1,3x |
| Jakarta-Bandung 148 km | 148,7 km / 6428 s | 167,0 km / 9042 s | tanpa tol: 1,4x waktu, butuh ε 2,0 |

### 13b. Profil motor: sisa pekerjaan (S)

- `motorcycle:conditional`, `access:conditional` (jam ganjil-genap, dsb) belum.
- Bobot gang: satu `footway` = 10 km/h untuk semua; turunkan dari `surface`/
  `smoothness` kalau data cukup.
- Motor belum punya perilaku "menerobos kemacetan" (traffic tidak dimodelkan),
  jadi rute motor = rute mobil yang lebih lambat di jalan kecil.

### 14. Konektor ferry (M), selesai (M5)

**Hasil.** Tidak perlu sub-graph: `route=ferry` sudah jadi edge biasa (sejak
awal), yang kurang hanya (a) waktu dari tag `duration`, sekarang dipakai
(`Merak-Bakauheni` 2 jam, bukan 30 km / 20 km/jam) dan (b) penanda. Penanda
disimpan sebagai **daftar edge sparse** (`uint32[ferryCount]`, ~10 rb entri
se-Indonesia), bukan array penuh: percobaan pertama (bit di `GL` uint16/edge)
menambah 93 MB di Java tanpa manfaat. Respons memuat `ferry_m`, `has_ferry`,
`ferry_spans` (rentang indeks ke `points`/polyline).

Sisa: ferry yang way-nya terpotong batas extract Geofabrik (node ujung tidak
tersambung ke jalan) ditangani tabel connector statis di item 17b; tidak ada
deteksi ferry otomatis.

### 15. Kecilkan artefak (M), selesai (M5)

**Hasil.** Format v4: `Off` uint32 (-4 B/node), `Len` uint16 satuan 4 m
(-2 B/edge), section `Time` dihapus -> `Cls` uint8 (-3 B/edge). Java 1.019 ->
**693 MB** (-32%), Sumatra 533 -> 364 MB, tujuh region 1.657 MB. `GO`/`GL`
varint tidak perlu: keduanya kosong (geometri interior belum ditulis).

Sisa (ide v5, kalau 800 MB per region mau ditekan lagi): koordinat 24-bit
offset per region (-2 B/node), `GNodes` grid 3 B/node (-1 B/node), atau
delta+varint per blok 64k dengan indeks blok (butuh desain akses acak).

### 17b. Stitching antar-file (M), selesai dengan catatan

**Masalah.** `-graph data/java.graph,data/sumatra.graph` memilih region dari snap
dan membalas `422` kalau kedua ujung beda region, padahal ferry Merak-Bakauheni
ada di salah satu file.

**Hasil.** Tabel connector statis berisi Merak-Bakauheni dan Ketapang-Gilimanuk.
Handler mencoba satu graph lebih dulu (extract yang tumpang tindih), lalu
merutekan start -> pelabuhan A dan pelabuhan B -> tujuan, menyambung ferry, dan
menggabung satu polyline dengan `legs[]` serta `ferry_spans`. Pasangan lain tetap
`422 no ferry connector`. Metric baru: `route_cross_region_stitched_total`.

Smoke `java.graph,sumatra.graph` dengan `-max-route-km 0`: Jakarta -> Palembang
200, 533,5 km, 3 leg, 0,35 s, tanpa `relaxed`; Jakarta -> Medan 200, 1.941,9 km,
3 leg, 3,2 s, `relaxed=true`. Leg Sumatra 1.797 km masih menabrak batas
ekspansi, jadi kriteria "tanpa relaxed kalau bisa dihindari" belum lulus tanpa
pekerjaan W6. Default `-max-route-km 40` tidak diubah; rute antarpulau harus
menaikkan flag.

**Sisa.** Konektor baru butuh entri tabel dan verifikasi koordinat pelabuhan;
tidak ada pencarian ferry otomatis. Graph nasional tetap alternatif deployment
besar (1,65 GB, 8 menit).

### 13c. Budget ekspansi & hierarki (M)

**Temuan M5.** Koridor panjang butuh ε 3,0 dan 4,8 jt ekspansi (Jakarta-Medan
19-22 s, 4-5 GB graph nasional di page cache). Tangga ε menutupi masalah, tapi
ini tanda batas: rute >800 km sebaiknya lewat hierarki (highway graph dulu,
lalu detail di sekitar ujung), bukan menaikkan `maxExpansions`.

### 14b. Jadwal ferry (S)

`duration` OSM sering kosong atau kasar; untuk Merak-Bakauheni hasilnya 1,45 jam
(OK), tapi banyak lintasan kecil tanpa tag sama sekali (jatuh ke 20 km/jam).
Kalau akurasi ferry penting, tabel statis per lintasan lebih jujur daripada OSM.

### 15b. Profil motor untuk 7 region, selesai (M5)

Ketujuh region punya artefak motor: Java 682 MB, Sumatra 349, Kalimantan 207,
Sulawesi 167, Bali-Nusra 148, Maluku 25, Papua 35. Total 1,6 GB untuk 7 region
(dibangun 1m09s..3m53s per region, 4 worker).

Sisa: server belum bisa mengalihkan profil per-request; satu proses = satu
profil (dua file graph, dua container, atau buka dua region dengan nama
`java-motor`). Kalau perlu satu proses dua profil, `region` harus menyimpan
pasangan graph dan `pickSnap` memilih berdasarkan param `profile`.

### 16a. Restriction per kelas kendaraan, selesai (E1 audit)
**Hasil.** Pemilihan tag kini mengikuti profil: car `restriction:motorcar` ->
`restriction:motor_vehicle` -> `restriction`; motor `restriction:motorcycle` ->
`restriction:motor_vehicle` -> `restriction`; `except` dievaluasi per profil.
Rebuild Java: usable 4.198 -> 4.202, ban 3.904 -> 3.907, skipped 596 -> 593;
600/600 jarak & durasi E1 identik.

**Rebuild setelah audit-002 (2026-11-08).** Satu PBF yang sama, extractor baru:
`seen=4654 usable=4078` (ban 3.924, only 154), `skipped=576`
(`not_car=200 not_class=14 missing=62 conditional=167 except=16 bad_type=1
unresolved=72 chain=8 via_way_missing=36`). `usable + skipped = seen` sekarang
selalu benar: dulu `Usable` dihitung sebelum aturan benar-benar tersimpan,
sehingga 141 relation terhitung dua kali. Via way yang tidak ada di graf pindah
dari `not_car` ke `via_way_missing`. Junction via way kini dicari lewat endpoint
yang benar, jadi 17 aturan via-way tambahan tersimpan (3.907 -> 3.924 ban).
Maluku tidak berubah sama sekali (45 relation, 42 usable, ban 22, only 20).
`profile_sha256` ikut berubah karena hash kini dihitung per profil. Angka 16a di
atas adalah sebelum filter reachability item 16; hasil akhir Java ada di item 16.

### 16b. Restriction conditional (M)
**Masalah.** 167 relation hanya punya `restriction:conditional` (jam sekolah,
jam pasar); graf statis tidak bisa mengevaluasi waktu. Saat ini di-skip dan
dihitung sebagai `conditional`.
**Pilihan.** (a) dokumentasikan sebagai batasan (status sekarang), (b) simpan
jendela waktu per ban di format v5 + evaluasi jam lokal request, (c) terapkan
larangan tanpa syarat (konservatif, bisa menutup jalan di luar jam).
**Lulus bila.** Pilihan eksplisit + angka dampak pada 30 pasangan yang
melintasi jalan dengan conditional.

### 16c. Penalti belok, selesai sebagian (keputusan produk: 0,25x)
**Masalah.** routeid meminimalkan waktu murni; OSRM meminimalkan `weight`
(durasi + penalti belok + preferensi kelas). Akibatnya p95 selisih jarak E1
21,05% walau rute routeid 1-3% lebih cepat di modelnya sendiri
(`RESULTS.md` §14). Detour 15-30% lebih panjang untuk keuntungan waktu kecil
adalah pengalaman pengguna yang buruk.
**Hasil.** Penalti belok diimplementasikan (tabel 22,5°/67,5°/135° =
2/7,5/20 s, `-turn-penalty`, default 0,25x, masuk `TimeDs`). E1 diulang pada
0/0,25/1/2x (`RESULTS.md` §15): p95 **tetap 21,12%** di 0,25x dan naik ke
22,02% di 1x; mean 3,60% -> 3,50%, p75 3,03% -> 2,82%, p90 11,20% -> 10,56%,
ETA +1,6% median (0,25x). 1x memperbaiki outlier ekstrem (max 48,30% ->
45,17%) tetapi merusak pasangan menengah.
**Kriteria lulus (p95 turun tanpa ETA +2%) tidak tercapai** untuk p95; dipilih
0,25x sebagai tie-break termurah. Sisa selisih -> 16d.

### 16d. Kalibrasi tabel kecepatan kelas jalan (M)
**Masalah.** Ekor p95 E1 (21%) tidak bergerak oleh penalti belok
(`RESULTS.md` §15). Routeid 1-3% lebih cepat di modelnya sendiri dengan jarak
15-30% lebih panjang: tabel kecepatan per kelas (dipakai juga oleh OSRM
sebagai `maxspeed` + `car.lua`) berbeda jauh pada jalan kolektor/lokal.
**Rencana.** Bandingkan kecepatan efektif per kelas antara model routeid dan
`car.lua`; kalibrasi tabel dari data E1/E2 (bukan dari intuisi); ukur ulang
p95 jarak + durasi. Perubahan tabel mengubah `ProfileHash` tetapi bukan format
graf, jadi rebuild hanya perlu bila `Len`/`Cls` berubah.
**Status 2026-11-10.** Tabel mobil disamakan dengan `car.lua` OSRM dan graph
Java, Sumatra, serta Nusa Tenggara dibangun ulang (`profile_sha256=c2240e41...`).
Pada 332 pasangan E1: median |selisih| 0,23%, p95 14,49%, maksimum 70,44%.
Gerbang jarak belum lulus; D4 mengganti metrik primer ke p95 durasi absolut
10,22% secara eksplisit. Audit 20 outlier ada di
`paper/results/e1-outliers-2026-11-10.csv`.

**Lulus bila.** p95 selisih jarak < 12% pada 332 pasangan E1 tanpa menaikkan
durasi median > 2%, atau metrik primer E1 diganti durasi secara eksplisit.

### 16. Restriction via-way (M), selesai (E1)

**Masalah.** 3,17% pasangan jadi tak terjangkau karena via-way dimodelkan
konservatif, lalu server retry tanpa larangan (`fallback`).

**Rencana.** Saat build, cek reachability: terapkan ban hanya bila ada jalur
alternatif dari edge masuk ke edge keluar. Kalau tidak ada, tandai relaksasi
sejak awal (tanpa retry saat runtime).

**Hasil 2026-11-10.** `buildRestrictions` mengumpulkan semua kandidat ban, lalu
`filterDetours` menjalankan BFS edge-state dari tail edge masuk ke head edge
keluar. BFS menegakkan seluruh ban dan aturan `only_*` lain, dengan batas 1 juta
edge-state. Ban tanpa jalur alternatif, termasuk yang mencapai batas, dibuang
dan dihitung `relaxed=`; pencarian yang mencapai batas dihitung `reach_cap=`.
Pada Java: 4.654 relasi, 3.962 aturan tersimpan, 3.808 ban, 154 `only_*`,
116 relaksasi (57 karena cap). `rid-restcheck`: 3.463 sampel, 0 pelanggaran,
0 fallback, 0 error (sebelumnya 113 fallback = 3,17%). Harness hanya menguji ban
yang tersimpan; 116 aturan yang dilonggarkan tidak masuk sampel.

**Lulus bila.** Fallback <= 1% pada harness `rid-restcheck`, pelanggaran tetap 0.
**Status: lulus (0%).**

---

## Demo publik

### 18. Peta coba publik (M), ide

**Masalah.** Saat ini orang hanya bisa mencoba routeid lewat curl/Bruno/Postman
atau integrasi Flutter. Belum ada halaman yang bisa dibuka di browser dan
langsung menunjukkan rute di peta, jadi calon pengguna harus menulis query
manual dan menempel GeoJSON ke geojson.io.

**Rencana.**
1. Halaman statis di `web/` (HTML + JS + CSS, Leaflet di-vendor, tanpa CDN) yang
   di-embed ke `rid-gateway` lewat `go:embed`; `GET /` menyajikan peta dan
   `/assets/*` menyajikan aset. Tidak ada container baru.
2. Interaksi: klik pertama = asal, klik kedua = tujuan; marker bisa digeser;
   tombol Rute memanggil `/route?format=geojson&from=...&to=...&profile=...`
   di origin yang sama, tanpa CORS dan tanpa API key.
3. Panel hasil: jarak, durasi, jarak snap, serta badge `fallback`, `relaxed`,
   dan `ferry`; error 422/429/504 tampil sebagai pesan, bukan JSON mentah.
4. URL state: `/?from=lat,lng&to=lat,lng&profile=motor` diperbarui lewat
   `replaceState` dan tombol salin tautan; membuka ulang URL memulihkan peta
   dan rute.
5. Mobile: layout satu kolom, target sentuh >= 44 px, peta tetap bisa digeser,
   panel hasil tidak menutupi tombol.
6. Tile raster OSM dengan atribusi, tanpa prefetch dan tanpa geocoder. Ini demo
   kecil; kalau trafik naik, pindah ke PMTiles/Protomaps atau tile berbayar
   daripada menambah tile server ke repo.
7. Deploy di belakang HTTPS (Caddy/tunnel yang sama dengan gateway). `/metrics`
   tidak diekspos ke publik; rate limit 15/menit per IP tetap berlaku.

**Lulus bila.** Dari browser ponsel, buka `/`, klik dua titik, dan dapat garis
serta jarak/durasi < 2 s untuk rute 10 km. Toggle mobil/motor mengubah rute; URL
yang dibagikan memulihkan titik dan profil; 422/429/504 tampil jelas; `go test`
tetap lulus dan binary tetap satu file. Tidak ada API key di client.

**Biaya.** M.
**Risiko.** Kebijakan tile OSM dan kuota publik; mitigasi: atribusi, tanpa
prefetch, batas zoom, dan siap pindah tile source.

---

## Catatan lintas item

- **Format berubah** di item 1, 4, 6, 13, 14, 15. Naikkan versi sekali di M1 dan
  rancang flag agar seksi opsional bisa absen (server toleran selama metadata
  cocok).
- **Setiap item yang mengubah rute wajib** menjalankan golden test M1 dan
  memperbarui `paper/RESULTS.md`.
- **Jangan tambah dependency eksternal** (libosrm, tile server, DB). Identitas
  proyek adalah satu binary statis.
