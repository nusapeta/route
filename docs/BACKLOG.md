# Backlog teknis

Rincian item dari [`ROADMAP.md`](ROADMAP.md). Urutan di sini mengikuti prioritas
milestone, bukan abjad. Format tiap item: masalah -> bukti -> rencana -> kriteria
lulus -> biaya.

Ukuran biaya: **S** = < setengah hari, **M** = 1-2 hari, **L** = 3-5 hari.

---

## M1. Kepercayaan

### 1. Metadata di header graph (S)

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

### 2. Build info di binary (S)

**Masalah.** Tidak ada cara mengetahui binary mana yang sedang jalan di homelab
(rsync menimpa `bin/static`).

**Rencana.** `-ldflags "-X main.commit=... -X main.buildTime=..."` di `Makefile`,
tampilkan di `/healthz` dan `/version`.

**Lulus bila.** `/healthz` menyebut commit 7 karakter yang ada di `git log`.

### 3. Golden test regresi rute (S)

**Masalah.** Setiap perubahan (snap, ALT, urutan) bisa menggeser rute tanpa
ketahuan. Sekarang tidak ada test yang menangkap itu.

**Rencana.** `testdata/golden-pairs.csv` (50 pasangan, berbagai kelas) + file
harapan berisi `distance_m`, `points_n`, dan hash FNV polyline. Test membaca
graph kecil (`data/test.graph` yang di-commit, ~2 MB) supaya CI tidak butuh pbf.

**Lulus bila.** Mengubah bobot atau snap tanpa sengaja membuat test gagal dengan
diff per pasangan.

---

## M2. Akurasi ujung rute

### 4. Snap ke ruas — SELESAI (M)

**Masalah.** `internal/graph/snap.go` memilih node terdekat. Di jalan yang
way-nya jarang titik, titik user jatuh ke node 100-300 m jauhnya -> awal/akhir
rute terlihat "mundur".

**Rencana.**
1. Tambah seksi grid-edge: untuk setiap sel, daftar edge yang menyentuh sel.
2. Kandidat terbaik = proyeksi tegak lurus ke segmen (a,b), jarak titik-ke-ruas,
   plus parameter t.
3. Ujung rute: gabungkan rute A* dengan dua bagian parsial — separator dari
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
satu arah), offset ditambahkan sebagai spur lurus — masih garis menyeberang
bangunan sepanjang `Along`. Menghilangkan ini butuh pseudo-node (opsi 2).

### 5. Ukur galat snap, bukan hanya klaim — SELESAI (S)

**Masalah.** Belum ada angka galat ujung sama sekali.

**Hasil.** `rid-snapcheck` sekarang melaporkan node vs ruas vs oracle, dengan
bucket panjang ruas, dan `-bbox` supaya bisa mengukur satu kota tanpa memuat
seluruh graf ke memori. Angkanya di `paper/RESULTS.md` §9.

---

## M3. Densitas

### 6. ALT landmark (L)

**Masalah.** Saturasi 4 query = 3,6 core-ekuivalen pada A* dengan eps 1.5. Cara
menaikkan RPS tanpa core tambahan hanya mengurangi ekspansi.

**Rencana.**
1. `rid-extract -landmarks 16`: pilih landmark deterministik (farthest-point
   sampling dari grid 4x4), jalankan Dijkstra dari/tujuan tiap landmark saat build.
2. Simpan `DistLandmark[16][node]` (uint32; Infinity = tak terjangkau) + untuk
   graf berarah juga jarak ke landmark (arah balik).
3. Heuristik: `max_l max(|d(l,v) - d(l,t)|, |d(v,l) - d(t,l)|)`, di-scale eps.
   Tetap admissible karena Dijkstra penuh.
4. Ukur: ekspansi, latency, RSS tambahan (16 x 4 B x 22,7 jt = 145 MB untuk Java
   — **terlalu besar**; alternatif: 8 landmark uint32 = 72 MB, atau simpan
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

### 7. Bidirectional A* (M)

**Masalah.** Frontier satu arah besar; tiap node yang di-pop menambah kerja.

**Rencana.** CSR balik (`Roff`/`Rto`), hentikan saat kedua frontier bertemu,
heuristik tetap admissible (`(d(v,t)-d(s,v))/2`).

**Status M3: ditunda, biaya memori lebih besar dari perkiraan awal.** CSR balik
butuh `Rto` uint32 per edge (24 jt x 4 B = 96 MB Sumatra, 187 MB Java) plus
`Roff` per node (11,9 jt x 4 B = 48 MB Sumatra, 91 MB Java) — total **144 MB
Sumatra / 278 MB Java**, di atas batas RSS beban 350 MB sebelum workspace.
Dua arah juga menggandakan frontier, jadi penghematan workspace hanya ~25%.
Opsi berikutnya kalau tetap dikerjakan: reverse CSR sebagai sidecar opt-in
sama seperti ALT, dibangun `rid-extract -reverse` setelah graph utama.

**Lulus bila.** Ekspansi turun >= 30% untuk rute 15-42 km tanpa perubahan jarak.

### 8. Prealokasi workspace A* (S)

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

### 9. Timeout & pembatalan (S)

**Masalah.** `cmd/rid-server/main.go` tidak punya timeout per request; query
579 km menahan slot ~2 s, dan klien bisa menumpuk sampai memori naik.

**Rencana.** `http.TimeoutHandler` 10 s + `context.WithTimeout` diteruskan ke
`Router`, dengan cek `ctx.Err()` di loop A* setiap 4096 pop. Setelah timeout,
kembalikan `504` berisi `{"error":"deadline exceeded"}`.

**Lulus bila.** Query yang dipaksa lambat berhenti <= 10 s dan slot langsung
tersedia untuk request berikutnya.

### 10. Rate limit & bind (S)

**Rencana.** Pembatas token-bucket per IP (15/menit, burst 5) sebagai middleware;
`-bind 127.0.0.1` default di `deploy/docker-compose.yml` + Caddy `reverse_proxy`.

**Lulus bila.** `ab`/loop 100 request dari satu IP menghasilkan `429` setelah
jatah, tanpa memengaruhi IP lain.

### 11. `/metrics` (S)

**Rencana.** Counter: `route_requests_total{format,status}`,
`route_duration_seconds` (histogram), `astar_expansions_sum`,
`restriction_fallback_total`, `region_select_total{region}`.

**Lulus bila.** `curl /metrics` menampilkan angka yang cocok dengan 1 request
yang dilakukan manual.

### 12. ETag & cache (S)

**Rencana.** Hash dari `(from,to,format,simplify,version graph)`; balas `304`
bila `If-None-Match` cocok; `Cache-Control: public, max-age=60`.

**Lulus bila.** Request kedua identik balas `304` tanpa menjalankan A*.

---

## M5. Cakupan

### 13. Profil motor (M) — selesai (M5)

**Hasil.** Dua artefak, bukan satu graph dengan bobot ganda: `-profile car|motor`
saat build. Alasannya diukur: kelas kecepatan motor menambah 2,3 M node / 1,5 M
edge (jalan/gang yang boleh motor) sehingga `data/java-motor.graph` = 714 MB vs
693 MB — lebih murah daripada menyimpan dua tabel waktu per edge di runtime.

| rute | mobil | motor | catatan |
|---|---|---|---|
| Jakarta-Cikarang 42 km | 42,4 km / 1941 s | 40,8 km / 2525 s | motor menghindari tol, waktu 1,3x |
| Jakarta-Bandung 148 km | 148,7 km / 6428 s | 167,0 km / 9042 s | tanpa tol: 1,4x waktu, butuh ε 2,0 |

### 13b. Profil motor — sisa pekerjaan (S)

- `motorcycle:conditional`, `access:conditional` (jam ganjil-genap, dsb) belum.
- Bobot gang: satu `footway` = 10 km/h untuk semua; turunkan dari `surface`/
  `smoothness` kalau data cukup.
- Motor belum punya perilaku "menerobos kemacetan" (traffic tidak dimodelkan),
  jadi rute motor = rute mobil yang lebih lambat di jalan kecil.

### 14. Konektor ferry (M) — selesai (M5)

**Hasil.** Tidak perlu sub-graph: `route=ferry` sudah jadi edge biasa (sejak
awal), yang kurang hanya (a) waktu dari tag `duration` — sekarang dipakai
(`Merak-Bakauheni` 2 jam, bukan 30 km / 20 km/jam) dan (b) penanda. Penanda
disimpan sebagai **daftar edge sparse** (`uint32[ferryCount]`, ~10 rb entri
se-Indonesia), bukan array penuh: percobaan pertama (bit di `GL` uint16/edge)
menambah 93 MB di Java tanpa manfaat. Respons memuat `ferry_m`, `has_ferry`,
`ferry_spans` (rentang indeks ke `points`/polyline).

Sisa: ferry yang way-nya terpotong batas extract Geofabrik (node ujung tidak
tersambung ke jalan) masih butuh graph nasional atau stitching antar-file —
lihat item 17b.

### 15. Kecilkan artefak (M) — selesai (M5)

**Hasil.** Format v4: `Off` uint32 (-4 B/node), `Len` uint16 satuan 4 m
(-2 B/edge), section `Time` dihapus -> `Cls` uint8 (-3 B/edge). Java 1.019 ->
**693 MB** (-32%), Sumatra 533 -> 364 MB, tujuh region 1.657 MB. `GO`/`GL`
varint tidak perlu: keduanya kosong (geometri interior belum ditulis).

Sisa (ide v5, kalau 800 MB per region mau ditekan lagi): koordinat 24-bit
offset per region (-2 B/node), `GNodes` grid 3 B/node (-1 B/node), atau
delta+varint per blok 64k dengan indeks blok (butuh desain akses acak).

### 17b. Stitching antar-file (M)

**Masalah.** `-graph data/java.graph,data/sumatra.graph` memilih region dari snap
dan membalas `422` kalau kedua ujung beda region, padahal ferry Merak-Bakauheni
ada di salah satu file.

**Rencana.** Tabel connector per pasangan region (node pelabuhan A, node
pelabuhan B, jarak/waktu ferry), lalu rute dijalankan per-leg dan digabung jadi
satu respons (satu polyline, `legs[]`). Alternatif: satu graph nasional
(1,65 GB, sudah terbukti bisa dibangun 8 menit) untuk deployment besar.

**Lulus bila.** Jakarta -> Medan 200 tanpa file >800 MB tanpa `relaxed=true`
kalau bisa dihindari.

### 13c. Budget ekspansi & hierarki (M)

**Temuan M5.** Koridor panjang butuh ε 3,0 dan 4,8 jt ekspansi (Jakarta-Medan
19-22 s, 4-5 GB graph nasional di page cache). Tangga ε menutupi masalah, tapi
ini tanda batas: rute >800 km sebaiknya lewat hierarki (highway graph dulu,
lalu detail di sekitar ujung), bukan menaikkan `maxExpansions`.

### 14b. Jadwal ferry (S)

`duration` OSM sering kosong atau kasar; untuk Merak-Bakauheni hasilnya 1,45 jam
(OK), tapi banyak lintasan kecil tanpa tag sama sekali (jatuh ke 20 km/jam).
Kalau akurasi ferry penting, tabel statis per lintasan lebih jujur daripada OSM.

### 15b. Profil motor untuk 7 region — selesai (M5)

Ketujuh region punya artefak motor: Java 682 MB, Sumatra 349, Kalimantan 207,
Sulawesi 167, Bali-Nusra 148, Maluku 25, Papua 35. Total 1,6 GB untuk 7 region
(dibangun 1m09s..3m53s per region, 4 worker).

Sisa: server belum bisa mengalihkan profil per-request — satu proses = satu
profil (dua file graph, dua container, atau buka dua region dengan nama
`java-motor`). Kalau perlu satu proses dua profil, `region` harus menyimpan
pasangan graph dan `pickSnap` memilih berdasarkan param `profile`.

### 16. Restriction via-way (M)

**Rencana.** Tabel kecepatan kedua (motor 30-50 km/h di kelas kecil), akses:
`motorcycle=no` ditolak, `motor_vehicle` dihormati, `service` dan gang lebih
permisif. Pilih lewat param `profile=motorcar|motorcycle`; graph tetap satu,
bobot dibaca dari tabel berbeda (butuh waktu tempuh per edge per profil: simpan
`Time` mobil saja, hitung motor dari `Len` + kelas jalan saat load).

**Lulus bila.** Contoh rute gang sempit di Yogyakarta mengembalikan rute yang
tidak memutar lewat jalan arteri, dan tabel kecepatan terdokumentasi di README.

### 14. Konektor ferry (M)

**Rencana.** Ekstrak relasi `route=ferry` menjadi edge dengan atribut `ferry=1`
dan durasi jadwal kasar; hubungkan graf per region pada node pelabuhan. Batasi
hanya lintasan yang didefinisikan.

**Lulus bila.** Jakarta -> Medan mengembalikan rute dengan penanda ferry, dan
rute tanpa ferry tidak berubah (golden).

### 15. Kecilkan artefak (M)

**Rencana.** `Time` (uint32, 4 B/edge) diturunkan dari `Len` + 1 B kelas jalan
-> hemat 187 MB di Java. `GO`/`GL` digabung varint. Naikkan versi format.

**Lulus bila.** Graph Java <= 800 MB, waktu muat & latency tidak berubah > 5%.

### 16. Restriction via-way (M)

**Masalah.** 3,17% pasangan jadi tak terjangkau karena via-way dimodelkan
konservatif, lalu server retry tanpa larangan (`fallback`).

**Rencana.** Saat build, cek reachability: terapkan ban hanya bila ada jalur
alternatif dari edge masuk ke edge keluar. Kalau tidak ada, tandai relaksasi
sejak awal (tanpa retry saat runtime).

**Lulus bila.** Fallback <= 1% pada harness `rid-restcheck`, pelanggaran tetap 0.

---

## Catatan lintas item

- **Format berubah** di item 1, 4, 6, 13, 14, 15. Naikkan versi sekali di M1 dan
  rancang flag agar seksi opsional bisa absen (server toleran selama metadata
  cocok).
- **Setiap item yang mengubah rute wajib** menjalankan golden test M1 dan
  memperbarui `paper/RESULTS.md`.
- **Jangan tambah dependency eksternal** (libosrm, tile server, DB). Identitas
  proyek adalah satu binary statis.
