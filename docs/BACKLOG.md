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

**Catatan penting.** Karena biaya memorinya besar, ALT harus diuji terhadap
budget: kalau +72 MB menaikkan RPS 2x, densitas tetap naik (19,5 -> ~50 RPS/GB),
jadi masih layak; kalau hanya 1,2x, tolak dan pilih bidirectional.

**Lulus bila.** >= 20 RPS @42 km di i3-6100T, RSS beban <= 350 MB, jarak rute
identik (golden).

### 7. Bidirectional A* (M)

**Masalah.** Frontier satu arah besar; tiap node yang di-pop menambah kerja.

**Rencana.** CSR balik (`Roff`/`Rto`, ~11 MB Java), hentikan saat kedua frontier
bertemu, heuristik tetap admissible (`(d(v,t)-d(s,v))/2`). Cocok dipadukan
dengan eps yang ada.

**Lulus bila.** Ekspansi turun >= 30% untuk rute 15-42 km tanpa perubahan jarak.

### 8. Prealokasi workspace A* (S)

**Masalah.** Puncak heap 876 MB setelah 29 rute panjang karena array tumbuh
(grow menyalin). Ini juga sumber GC spike.

**Rencana.** Perkiraan ukuran dari jarak heuristik awal (`haversine/avgEdgeLen`),
dialokasikan sekali dengan faktor 1,3; kalau meleset, fallback ke map hash
(seperti sekarang) sehingga perilaku untuk rute ekstrem tak berubah.

**Lulus bila.** Rute 148 km memakai <= 60% heap sekarang; tidak ada regresi
latency > 5%.

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

### 13. Profil motor (M)

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
