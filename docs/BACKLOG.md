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
`-max-route-km` lama 40 km), jadi `422` anggaran ekspansi tetap 4-7 dan itu
batasan anggaran, bukan snap. Diulang pada image `81722ea` di homelab dengan pasangan
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
4. Ukur: ekspansi, latency, RSS tambahan. Aritmetika Java (22.712.775 node):
   satu array uint32 per landmark = 90,9 MB, jadi 16 landmark = **1,45 GB satu
   arah** (2,9 GB kalau arah balik ikut) — jelas tidak mungkin. Dengan uint16
   bersaturasi: 2 landmark x 2 arah = **182 MB**, 4 landmark x 2 arah = **364 MB**.

**Status M3: ditunda karena manfaatnya belum terbukti, bukan karena mustahil.**
Setelah M3 menurunkan biaya per ekspansi 1,9-2,0x (lihat RESULTS §10), ALT hanya
mengurangi ekspansi; 39% CPU trigonometri sudah hilang. Gerbang keputusannya:
kalau +182 MB menaikkan RPS 2x, kerjakan; kalau hanya 1,2x, tolak.

**Aritmetika memori (dikoreksi 2026-11-12; versi lama salah 10x di baris
uint32 dan lupa arah balik di baris uint16).** `d(l,·)` sebagai `uint16` (skala
1/8 detik, saturasi admissible karena `min(d,cap)` tetap batas bawah): per
landmark butuh dua array (dari dan ke landmark) = 90,9 MB Java. Jadi 2 landmark
= **182 MB**, 4 landmark = **364 MB**. Rencana sidecar tetap: file `map.alt`
terpisah (magic, jumlah landmark, id landmark, array uint16), dibuka hanya
kalau flag `-alt map.alt` diberikan.

**Biaya build terukur 2026-11-12 (bukan perkiraan).** Satu Dijkstra penuh
(heap 4-ary, tanpa turn cost) atas Java 22,7 jt node / 46,7 jt edge: 2,03 /
2,07 / 2,07 s, 22,6 jt pop per pass, single thread di Ryzen 5 5600G. ALT 2
landmark = 4 pass (maju + balik) ~= 8 s; 4 landmark ~= 17 s; tujuh region
selesai dalam hitungan menit. Build bukan penghalang.

**Baseline runtime 2026-11-12 (satu region Java, `-conc 4`, rute 42 km).**
`Rss` 141 MB, `Anonymous` 102 MB, tabel workspace 52,7 MB. 2 landmark: 141 +
182 = **323 MB**, masih di bawah anggaran 350 MB dengan sisa ~27 MB; 4 landmark:
505 MB, **tidak muat**. Karena itu kalau ALT dikerjakan, batasnya 2 landmark
(atau sidecar `mmap` yang RSS-nya hanya sebesar halaman tersentuh). Angka Rss
1,02-1,10 GB pada cap 100 km berasal dari tiga region ter-mmap, bukan heap
(lihat item 8).

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

**Koreksi bingkai 2026-11-12.** "Tertahan" di sini berarti keputusan produk,
bukan ketidakmungkinan: build reverse CSR hanya satu pass 46,7 jt edge (<1 s di
mesin ini, bandingkan Dijkstra penuh 2,07 s), dan TOTAL 278 MB Java itu nyata
(46.676.434 x 4 B = 186,7 MB `Rto` + 22.712.776 x 4 B = 90,9 MB `Roff`). Yang
menolak adalah anggaran 350 MB: 141 MB baseline + 278 MB = 419 MB. Karena itu
jalurnya sidecar opt-in dengan anggaran sendiri, atau diterima hanya untuk
deploy 1 region ber-`mem_limit` lebih besar.

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

**Hasil ukur 2026-11-12 (graph v4, `-conc 1 -workspace-mb 256`, dan batas jarak
dimatikan karena 148 km garis lurus melewati cap 100 km sekarang).**
Jakarta-Bandung 148 km: `200` dalam 2,04 s, `Anonymous`
164 MB, `Rss` 233 MB, tabel workspace 100.663.296 B / 8,39 jt slot (dari 22,3 jt
slot yang diizinkan). Diulang 20x
berturut-turut: `Anonymous` stabil di 163,7 MB, `Rss` 238,9 MB, tabel tidak
tumbuh lagi. Batas 60% dari puncak lama (876 MB) = 526 MB, jadi kriteria heap
lulus dengan margin ~3x; yang tersisa hanya "tidak ada regresi latency > 5%",
dan itu menunggu baseline latency dari graph yang sama. Untuk konteks, satu
region (Java, `-conc 4`) setelah rute 42 km: `Rss` 141 MB, `Anonymous` 102 MB —
masih di bawah anggaran 350 MB item 6/7.

**Hasil ukur 2026-11-12 (pelepasan tabel saat idle).** Tabel hasil grow tidak
pernah menyusut dengan `shrinkIfNeeded`: syaratnya `len(keys) > 8*used`, padahal
grow berhenti di load factor 0,8. Jadi satu rute kelas 100 km memaku tabel
puncak (2,6 jt slot per workspace) sampai proses mati. Sekarang `put()`
menstempel waktu idle dan reaper 15 s mengambil workspace yang menganggur >30 s,
mengembalikan tabel ke 65.536 slot, lalu satu `debug.FreeOSMemory()` supaya
halaman bekas tabel kembali ke kernel. Lokal: 8.519.680 slot (102 MB) -> 262.144
slot (3 MB) setelah 50 s idle, rute yang sama tetap `200` 2,41 s. Homelab:
`RssAnon` 143.500 -> 4.532 kB (`routeid`) dan 142.548 -> 5.796 kB
(`routeid-motor`), `VmRSS` 815.600 -> 75.888 kB. Latensi setelah idle tidak
turun kelas: 43 km 151-153 ms vs 155-173 ms sebelumnya. Kriteria "tidak ada
regresi latency > 5%" terpenuhi; puncak heap saat query tidak berubah.

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
`ctx.Err()` tiap 256 ekspansi (bukan 4096), dan membalas
`504 {"error":"deadline exceeded"}`. Default 10 s lewat `-timeout`; test handler
menutup kontrak ini.

**Revisi 2026-11-12: batas lunak lebih dulu.** Batas keras 10 s ternyata tidak
cukup: pencarian yang menghabiskan anggaran ekspansi (6 juta) butuh 12,8 s di
i3-6100T, jadi rute 80-100 km berakhir `504` alih-alih `422` dan statusnya
berubah mengikuti beban. Sekarang `-budget` (default 60% `-timeout` = 6 s)
menghentikan pencarian lebih dulu dan menjawab
`422 {"error":"search budget 6s exceeded (route too long)","reason":"budget"}`
(`route_errors_total{reason="budget"}`); `504` hanya untuk batas keras dan klien
yang memutus koneksi. `-max-expansions` (default 4 juta) tetap sebagai plafon
deterministik. Rincian angka dan alasan pemilihan ada di item 13c.

### 10. Rate limit & bind (S), selesai (M4)

**Rencana.** Pembatas token-bucket per IP (15/menit, burst 5) sebagai middleware;
bind loopback + reverse proxy/TLS di depan.

**Lulus bila.** `ab`/loop 100 request dari satu IP menghasilkan `429` setelah
jatah, tanpa memengaruhi IP lain.

**Hasil (M4).** Token bucket per IP (default 15/menit, burst 5) aktif, dengan
`-trust-proxy` untuk `X-Forwarded-For`; balasan `429` memuat `Retry-After`.

**Bind & TLS (2026-11-11).** Gateway dipetakan `127.0.0.1:8080:8080` di
`deploy/docker-compose.yml`; upstream tetap `127.0.0.1:18080/18081`. TLS dan
nama domain datang dari Cloudflare Tunnel milik 1Panel, bukan Caddy: tidak ada
port yang mendengarkan di `0.0.0.0`, dan `192.168.1.219:8080` menolak koneksi.
Karena edge Cloudflare, `-trust-proxy` mengambil `CF-Connecting-IP` lebih dulu
supaya bucket tetap per pengunjung, bukan per container `cloudflared`.

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
`route_errors_total{reason}` (label terpasang: `snap`, `no_route`, `too_far`,
`budget`, `cross_region`), `route_cross_region_*`, `route_snap_retry_total`,
dan `routeid_build_info`.

**Publik (2026-11-11).** Gateway compose memakai `-block-metrics`, jadi
`https://route.arifsani.xyz/metrics` menjawab `404`; scrape tetap langsung ke
upstream loopback `127.0.0.1:18080/metrics` dan `127.0.0.1:18081/metrics`
(`200`). Filter dilakukan di aplikasi, bukan lewat aturan path Cloudflare.

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

### 13c. Budget ekspansi & hierarki (M)

**Temuan M5.** Koridor panjang butuh ε 3,0 dan 4,8 jt ekspansi (Jakarta-Medan
19-22 s, 4-5 GB graph nasional di page cache). Tangga ε menutupi masalah, tapi
ini tanda batas: rute >800 km sebaiknya lewat hierarki (highway graph dulu,
lalu detail di sekitar ujung), bukan menaikkan `maxExpansions`.

**Kontrak 100 km (2026-11-12).** Setelah `-max-route-km` dinaikkan 40 -> 100,
8 dari 600 pasangan `pairs-java.csv` gagal `422 search space exhausted`
setelah 5-9 s (`paper/RESULTS.md` §19). Diulang dengan `-workspace-mb` 256/512/2048 hasilnya identik,
jadi yang habis `maxExpansions`, bukan tabel; menaikkan anggaran tabel tidak
menolong.
Sampai hierarki ada, biaya ini diterima dan didokumentasikan di
`paper/RESULTS.md` §19.

**Ekor 80-100 km dan 504 (ditutup 2026-11-12).** Bench 600 pasangan berhenti di
79,9 km garis lurus, jadi pita 80-100 km tidak pernah terukur. Di i3-6100T
pasangan 98,4 km menghabiskan 6 juta ekspansi dalam 12,8 s sehingga lewat
`-timeout 10s` dan dijawab `504`; pasangan 91,8 km selesai 10,01-10,02 s,
jadi statusnya 422 atau 504 tergantung beban. Biaya ekspansi i3: 3 jt = 9,0 s,
4 jt = 11,2 s, 6 jt = 12,8 s, sehingga tidak ada plafon statis yang selalu
mendahului batas waktu keras. Perbaikan yang dipakai: `-budget` 6 s (batas lunak
-> `422 search budget 6s exceeded`, metrik `reason="budget"`) plus
`-max-expansions` 4 juta sebagai plafon deterministik. 481 rute 200 pada bench
maksimum 3.186.176 ekspansi (p99 2,73 jt, median 141 rb), jadi 4 juta tidak
memotong satu pun rute yang berhasil. Verifikasi homelab: 98,4 dan 91,8 km ->
422 pada 6,02 s, 96,4 km -> 200 0,18 s, `code="504"` tetap 0.

**Harga batas 6 s di i3 (600 pasangan, 2026-11-12).** Instance deploy vs
instance referensi (`-budget 0 -timeout 30s`), keduanya `-conc 4`: 472 vs 475
rute 200. Tiga pasangan yang di referensi 200 (7,75-9,06 s, 2,46-2,99 jt
ekspansi) menjadi `422 search budget`; 13 lainnya memang `422` di referensi
(8 `exhausted` + 5 `too_far`). p99 turun 4,52 -> 2,94 s, maksimum 9,06 -> 4,74
s (`paper/RESULTS.md` §19). Keputusan: pertahankan 6 s — menyelamatkan ketiga
pasangan itu butuh anggaran ~9,1 s, yaitu menghapus margin ke `-timeout 10s`
dan mengembalikan `504` yang bergantung beban. Angka ini juga menjadi batas
bawah untuk hardware lebih lambat: kalau mesin 4x lebih lambat, kerugian
sebanding naik.

### 13d. Radix heap, 16-bit tile-local, MLD (M), ditunda

Ketiganya disetujui sebagai kandidat optimasi keempat sampai keenam, lalu
diukur ulang setelah format v5 live. Semuanya berbenturan dengan gate golden,
bukan dengan anggaran waktu:

- **Radix heap.** Profil CPU 73 rute 50-80 km Java: `heapPop` 24,7% dan
  `workspace.probe` 25,3%; heap sudah 4-ary. Uji sensitivitas urutan tie:
  mengganti arity 4 -> 8 mempertahankan `dist`/`time` semua pasangan golden
  tetapi mengubah `seq_hash` (pair 90: `fe8648…` -> `a48b4a…`). Artinya
  struktur antrean apa pun yang mengubah urutan key sama akan gagal golden,
  dan re-pin dilarang. Radix juga menuntut key insert >= key terakhir yang
  keluar; heuristik berbobot `eps=1.5` tidak konsisten sehingga syarat itu
  tidak dijamin.
- **16-bit tile-local + delta CSR.** Target 16-bit hanya muat kalau node
  dinomori ulang per tile. Itu mengubah id node, jadi `seq_hash` golden ikut
  berubah; menyimpan tabel pemetaan id global (4 B/node = 94 MB) menghapus
  seluruh penghematan `To` (4 -> 2 B/edge = 97 MB). Tanpa renumber, encoding
  yang aman hanya 24-bit (hemat 48 MB, 7% file), bukan target yang diminta.
- **MLD.** Overlay menghasilkan jarak/waktu yang sama, tetapi pemilihan jalur
  di antara biaya identik berbeda dari A* sehingga `seq_hash` gagal. Manfaat
  produknya juga nol selama `-max-route-km` 100: semua rute di atas cap sudah
  dijawab 422 sebelum pencarian. Hierarki tetap relevan hanya kalau cap
  dinaikkan (lihat 13c).

Kalau gate golden dilonggarkan kelak, urutan kerja yang masuk akal: MLD
dengan uji jarak/waktu saja, lalu radix heap, lalu format tile-local. Sampai
itu ketiganya sengaja tidak dikerjakan.

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

### 14b. Jadwal ferry (S)

`duration` OSM sering kosong atau kasar; untuk Merak-Bakauheni hasilnya 1,45 jam
(OK), tapi banyak lintasan kecil tanpa tag sama sekali (jatuh ke 20 km/jam).
Kalau akurasi ferry penting, tabel statis per lintasan lebih jujur daripada OSM.

### 15. Kecilkan artefak (M), selesai (M5)

**Hasil.** Format v4: `Off` uint32 (-4 B/node), `Len` uint16 satuan 4 m
(-2 B/edge), section `Time` dihapus -> `Cls` uint8 (-3 B/edge). Java 1.019 ->
**693 MB** (-32%), Sumatra 533 -> 364 MB, tujuh region 1.657 MB. `GO`/`GL`
varint tidak perlu: keduanya kosong (geometri interior belum ditulis).

Sisa (ide v5, kalau 800 MB per region mau ditekan lagi): koordinat 24-bit
offset per region (-2 B/node), `GNodes` grid 3 B/node (-1 B/node), atau
delta+varint per blok 64k dengan indeks blok (butuh desain akses acak).

### 15b. Profil motor untuk 7 region, selesai (M5)

Ketujuh region punya artefak motor: Java 682 MB, Sumatra 349, Kalimantan 207,
Sulawesi 167, Bali-Nusra 148, Maluku 25, Papua 35. Total 1,6 GB untuk 7 region
(dibangun 1m09s..3m53s per region, 4 worker).

Sisa: satu proses tetap satu profil. Pengalihan per-request sekarang
ditangani `rid-gateway` di depannya (`routeid` mobil di 18080, `routeid-motor`
di 18081, satu base URL `https://route.arifsani.xyz` dengan `profile=motor`),
bukan oleh `rid-server`. Kalau kelak ingin satu proses dua profil, `region`
harus menyimpan pasangan graph dan `pickSnap` memilih berdasarkan param
`profile`.

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

### 16d. Kalibrasi tabel kecepatan kelas jalan (M), selesai lewat D4
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

**Status: lulus lewat klausa kedua (D4).** Gerbang jarak tidak tercapai (p95
14,49%), dan keputusan D4 mengganti metrik primer E1 ke p95 durasi absolut
10,22%; lihat `docs/ROADMAP.md` baris D4 dan `paper/RESULTS.md` §18. Tidak ada
pekerjaan kalibrasi tabel yang tersisa di backlog ini; tabel mobil sudah sama
dengan `car.lua` OSRM (`profile_sha256=c2240e41...`).

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
pekerjaan W6. Default `-max-route-km` 40 pada saat itu (kini 100, lihat
`paper/RESULTS.md` §19); rute antarpulau harus
menaikkan flag.

**Sisa.** Konektor baru butuh entri tabel dan verifikasi koordinat pelabuhan;
tidak ada pencarian ferry otomatis. Graph nasional sekarang **default deploy**
(2026-11-12): `data/indonesia.graph` 54.891.393 node / 112.340.071 edge,
v5 `car+motor`, 1.621 MB; lokal terukur 4x96 km paralel = 225 MiB RSS
(`anon` 90 MiB), Merak-Bakauheni 28,1 km `ferry_m=28079`. Semua pulau dilayani
dari satu file, tanpa connector. Batas 800 MB per artefak dikecualikan secara
eksplisit; `-max-route-km 100` tetap, jadi ini cakupan lokal seluruh Indonesia,
bukan rute Jakarta-Jayapura.

## Demo publik

### 18. Peta coba publik (M), selesai sebagian (kode + HTTPS publik)

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
   **[x] 2026-11-11:** live di `https://route.arifsani.xyz` lewat Cloudflare
   Tunnel 1Panel.

**Status (2026-11-10).** Langkah 1-6 selesai. `web/` di-embed ke `rid-gateway`;
`GET /` dan `/assets/*` dilayani same-origin dengan CSP dan `no-cache`, tanpa
container baru. Leaflet 1.9.4 di-vendor, tile OSM memakai atribusi, URL state
memulihkan rute, dan panel hasil hanya menampilkan field respons. Verifikasi
lokal di Chromium: rute Jakarta 2,39 km, 3 mnt, snap 4/16 m; ponsel 320 dan 360
px tanpa overflow, tombol 44 px, rute tetap di atas panel.

**Deploy publik (2026-11-11).** Stack Compose homelab aktif dengan gateway
hanya `127.0.0.1:8080`; LAN `192.168.1.219:8080` menolak koneksi. Image
`routeid:local` dari commit `ca6d050-dirty` (built
`2026-11-11T14:58:55Z`), rollback tag `routeid:pre-cloudflare`. Container
`cloudflared-tunnel` milik 1Panel sudah berjalan di `1panel-network`; gateway
diberi alias network `gateway` lewat `deploy/docker-compose.homelab.yml`, jadi
ingress `http://gateway:8080` resolve tanpa membuka port host. Tidak memakai
profile compose `cloudflare` karena connector kedua tidak bisa me-resolve
origin 1Panel lain.

Verifikasi publik: `https://route.arifsani.xyz/healthz` `200` dan melaporkan
mobil + motor sehat; `/version` mengembalikan `ca6d050-dirty`; `/`,
`/assets/app.js`, dan `/route` masing-masing `200`; `/metrics` `404`.
Rate limit per IP di gateway homelab: dua header `CF-Connecting-IP` berbeda
masing-masing `200` lima kali lalu `429`; dari publik, request berurutan
menghabiskan bucket yang sama. Scrape upstream `127.0.0.1:18080/metrics` tetap
`200`.

**Deploy 2026-11-12 (`b2f64cd`).** Image `routeid:local` dibangun dari commit
`b2f64cd` (label `b2f64cd-dirty` karena satu file Bruno milik pengguna masih
termodifikasi di mesin build). Verifikasi: 98,4 km -> `422 search budget 6s
exceeded` pada 6,01 s (dulu `504` pada 10,01 s), 96,4 km -> `200` 0,17 s,
`route_errors_total{reason="budget"}` 1, `code="504"` 0, dan rute 96,4 km yang
sama dari publik `200` 0,73 s. Rollback tetap `routeid:pre-cloudflare`.

**Deploy graph nasional (2026-11-12, `85880e8`).** `data/indonesia.graph`
(1.621 MB, SHA-256 `b0e0f237...`) dan compose baru disalin ke homelab; graph
lama tetap ada untuk rollback. `/healthz` publik melaporkan mobil dan motor
`region=indonesia` (54.891.393 node / 112.340.071 edge, format 5,
`pbf_sha256=752489d3...`). Tujuh pulau diuji dari publik dengan koordinat lokal:
Jakarta 9,0 km, Medan-Binjai 22,6 km, Balikpapan 7,3 km, Makassar-Maros 26,0 km,
Denpasar-Singaraja 82,5 km, Ambon 6,1 km, Jayapura 15,6 km; semua `200`.
Merak-Bakauheni 28,1 km, `ferry_m=28079`, `has_ferry=true`. Profil motor
Medan-Binjai 22,6 km / 1344,3 s, header `X-Route-Profile: motor`,
`X-Route-Region: indonesia`. `/metrics` publik tetap `404`; Jakarta-Medan
ditolak `422` dengan `reason=too_far`.

Setelah beban, `docker stats`: `routeid` 27,89 MiB / 1,5 GiB, `routeid-motor`
15,14 MiB, `routeid-gateway` 3,25 MiB; ketiganya `healthy`, restart 0. Idle
proses nasional terukur 11,1 MB RSS (`anon` 4,5 MB). Image gateway dibangun
ulang dari commit yang sama agar panel memakai `app.js` tanpa penempelan teks
internal; `/version` melaporkan `85880e8`.

**Catatan insiden.** Route Cloudflare sempat `502` dengan log
`lookup gateway ... no such host` karena cloudflared ada di `1panel-network`
sedangkan gateway hanya di `deploy_default`. Perbaikan: gateway ikut
`1panel-network` dengan alias `gateway`; tidak perlu mengubah route Cloudflare.

**Verifikasi ponsel (2026-11-12, viewport 360x740, sentuh).** Dari publik,
klik dua titik di Jakarta (-6,16923/106,79672 -> -6,20336/106,84479) lalu
"Hitung rute": 9,97 km / 11 mnt dalam 232 ms. Toggle "Motor" mengubah hasil ke
12 mnt dengan `profile=motor` di request dan region `java-motor` di panel. URL
state Monas -> Kuningan memulihkan rute (8,98 km, snap 311/6 m) dalam 319 ms;
"Salin tautan" menyalin URL yang sama; pin di laut (Teluk Jakarta) menampilkan
pesan 422 sebagai teks panel, bukan JSON; `scrollWidth` 360 = lebar viewport dan
semua tombol 44 px. Jadi kriteria ponsel di bawah lulus.

**Sisa.** `http://route.arifsani.xyz/healthz` masih menjawab `200` (dicek ulang
2026-11-12), belum redirect; aktifkan **Always Use HTTPS** di Cloudflare
(SSL/TLS -> Edge Certificates) kalau ingin HTTPS-only. Itu setelan dashboard,
bukan kode.

**Lulus bila.** Dari browser ponsel, buka `/`, klik dua titik, dan dapat garis
serta jarak/durasi < 2 s untuk rute 10 km. Toggle mobil/motor mengubah rute; URL
yang dibagikan memulihkan titik dan profil; 422/429/504 tampil jelas; `go test`
tetap lulus dan binary tetap satu file. Tidak ada API key di client.

**Biaya.** M.
**Risiko.** Kebijakan tile OSM dan kuota publik; mitigasi: atribusi, tanpa
prefetch, batas zoom, dan siap pindah tile source.

### 18b. Pesan 422 yang bisa dibaca pengguna (S), selesai

**Masalah.** Pin di luar jaringan jalan (laut, hutan, pulau kecil tanpa jalan di
graf) menjawab `422` dengan teks internal `no road within snap distance (region
not loaded?)`, dan panel demo menampilkannya apa adanya
("Gagal menghitung rute. Titik tidak bisa dirutekan. no road within snap
distance (region not loaded?)"). Tanda tanya "region not loaded?" membingungkan
pengguna yang memang menaruh pin di laut.

**Bukti.** Pin Teluk Jakarta `-6.02926,106.81664` -> `422` di publik
(2026-11-12). Pada 600 pasangan `pairs-java.csv` dengan cap 100 km, 72/600
(12%) gagal dengan pesan yang sama; angka itu identik di cap 40 km, jadi ini
bukan regresi batas jarak (`paper/RESULTS.md` §19).

Sejak batas lunak `-budget` (2026-11-12) ada dua teks internal lagi yang
tampil di panel: `search budget 6s exceeded (route too long)` dan
`search space exhausted (route too long)`.

**Rencana.** Pertahankan `error` mesin yang stabil (dipakai
`cmd/rid-server/distance_test.go`), tambah field `reason` bertipe di badan JSON
422 dan 504, lalu petakan ke kalimat Indonesia di
`web/static/app.js` (`routeErrorMessage`). Kosakata `reason` sama dengan label
`route_errors_total{reason}` supaya metrik dan klien tidak punya dua enum:
`snap` ("Titik terlalu jauh dari jalan. Geser pin ke jalan terdekat."),
`too_far` ("Rute lebih dari 100 km..."), `budget` ("Rute terlalu panjang untuk
dihitung..."), `no_route` (termasuk `search space exhausted`), `cross_region`,
dan `deadline` untuk 504. Server tidak perlu tahu bahasa; klien lain (Flutter)
boleh tetap memakai `error`.

**Lulus bila.** Panel demo menampilkan kalimat Indonesia tanpa istilah internal
untuk keenam `reason`, `error` lama tidak berubah, dan Bruno/Postman tetap
lulus tanpa penyesuaian assertion.

**Hasil (18b).** `httpErrReason` menambahkan field `reason` pada `422`/`504`;
konstanta reason dipakai bersama label `/metrics` supaya kosakatanya tidak
bercabang. Panel memetakan keenam reason ke kalimat Indonesia dan tidak lagi
menempelkan teks internal. Diverifikasi lewat HTTP lokal: Teluk Jakarta
(`snap`), Jakarta-Bandung 116 km (`too_far`), budget 1 ns (`budget`), timeout
1 ns (`deadline`), Sumatra-Nusa tanpa feri (`cross_region`), dan pasangan
`pairs-java.csv` yang memang `no route` (`no_route`). Teks `error` tetap:
`no road within snap distance (region not loaded?)`, `route too long: ...`,
`search budget ...`, `deadline exceeded ...`, `no ferry connector ...`, dan
`no route`.

**Biaya.** S.

---

## Catatan lintas item

- **Format berubah** di item 1, 4, 6, 13, 14, 15. Naikkan versi sekali di M1 dan
  rancang flag agar seksi opsional bisa absen (server toleran selama metadata
  cocok).
- **Setiap item yang mengubah rute wajib** menjalankan golden test M1 dan
  memperbarui `paper/RESULTS.md`.
- **Jangan tambah dependency eksternal** (libosrm, tile server, DB). Identitas
  proyek adalah satu binary statis.
