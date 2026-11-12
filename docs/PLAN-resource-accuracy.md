# Plan: resource serendah mungkin, akurasi setinggi mungkin

**North star (tidak berubah):** router jalan Indonesia yang muat di puluhan MB,
bukan gigabyte, dan tetap memberi rute <=50 km di bawah setengah detik.
**Prioritas:** akurasi adalah *gerbang*; setelah gerbang lulus, minimalkan
resource. Tidak ada trade-off akurasi untuk menghemat memori/latensi.

Dokumen ini adalah rencana eksekusi. Ia melengkapi `ROADMAP.md`; bila target di
sini dan di `ROADMAP.md` berbeda, angka di sini yang dipakai (lebih baru,
terukur 2026-11-08).

## 1. Baseline terukur (jangan diubah tanpa angka baru)

| metrik | nilai | sumber |
|---|---|---|
| RSS proses, 3 region, sebelum query | 7,7 MB (anon 2,3 MB) | `/proc/<pid>/smaps_rollup`, instance baru |
| RSS container setelah beban campuran | 629 MiB (anon 612, file 45, kernel 3) | cgroup `memory.current` + `memory.stat` |
| Halaman graf resident | ~45 MB (graf 1,21 GB di-`mmap`) | cgroup `file` |
| Tabel workspace | 12 B/slot; dobel saat load 80%; tidak mengecil | `internal/graph/astar.go` |
| Tabel setelah rute 148 km | 3,16 jt ekspansi -> 4 M slot = 48 MB/slot | instrumentasi 2026-11-08 |
| Tabel setelah rute 579 km | 1,93 jt ekspansi -> 4 M slot = 48 MB/slot | idem |
| Frontier heap A* | maks 25.621 item (~0,2 MB) | idem; bukan penyebab memori |
| Slot per proses | region x `-conc` (3 x 4 = 12 tabel) | `cmd/rid-server/main.go` |
| `maxSlots` | `1<<25` -> 384 MiB/slot worst case | `internal/graph/astar.go` |
| `GOMEMLIMIT` 1 GiB vs 512 MiB | anon 516 vs 459 MB | idem (efek kecil) |
| Latency E1 (332 pasang) | p50 35,1 ms / p95 522 ms | `paper/RESULTS.md` §13c |
| Selisih jarak E1 vs OSRM | median -0,005%, p95 21,05%, 38/332 >10% | §13b/§14 |
| Cakupan E1 | 332 cocok, 81 `422`, 187 keluar filter snap | §13b |
| Ekspansi median per bucket | 3k / 40k / 135k / 314k / 745k | §2 |

Model kasar memori: `RSS ~ file resident (~50 MB) + SUM(region)(-conc x 12 B x
tabel_terbesar(region)) + runtime/go gc`. Semua optimisasi memori di bawah
menyerang suku kedua.

## 2. Definisi "akurasi setinggi mungkin" (harus bisa difalsifikasi)

OSRM bukan ground truth, jadi E1 hanya bisa mengukur *selisih antar-engine*.
Karena itu akurasi dipecah jadi tiga tingkat, dan hanya tingkat 3 yang boleh
disebut "akurasi":

| tingkat | definisi | status |
|---|---|---|
| A. legalitas & konektivitas | rute tidak melanggar turn restriction, oneway, tidak lewat ferry darat, tidak menembus laut | sebagian terukur (§14, fallback 3,17%) |
| B. kemiripan antar-engine | selisih jarak/durasi vs OSRM MLD dan vs engine kedua | E1: p95 21,05% (gerbang 12% gagal) |
| C. kebenaran | dibandingkan ground truth independen (trace GPS / audit manual ruas) | **belum ada (E2)** |

Rencana menaikkan B dulu (murah, terukur), lalu membangun C minimal (mahal).
Tanpa C, klaim "akurat" tetap dilarang oleh `paper/COMPARISON.md`.

## 3. Workstream

### W0. Harness & observabilitas (prasyarat semua)

**Masalah.** Sekarang memori hanya bisa ditebak; tidak ada metrik workspace dan
tidak ada tes regresi anggaran memori.

**Rencana.**
1. Metrik `/metrics`: `routeid_workspace_slots{region,slot}`,
   `routeid_workspace_bytes{region}` (gauge, dibaca dari panjang tabel),
   `routeid_workspace_grows_total{region}`.
2. Skrip `scripts/memcheck.sh`: nyalakan server dengan flag tertentu, jalankan
   daftar pasangan (mis. `paper/results/pairs-java.csv` baris terpilih), baca
   cgroup/smaps, cetak CSV (anon, file, RSS, tabel bytes).
3. Tes regresi: graf sintetis kecil, rute terpanjang yang ada, assert RSS
   proses < anggaran dan tabel < `maxSlots`.

**Lulus bila.** Satu perintah menghasilkan tabel memori yang bisa dibandingkan
antar commit; CI menjalankan tes regresi memori (skala kecil, deterministik).

**Status (2026-11-08).** Metrik terkirim sebagai gauge **tanpa label region**
(`routeid_workspace_slots`, `routeid_workspace_bytes`,
`routeid_workspace_grows_total`, `routeid_workspace_shrinks_total`): sejak W1
tabel dipakai bersama lintas region, jadi label `region` akan menyesatkan.
`scripts/memcheck.sh` hanya membaca `/metrics` + `smaps_rollup`; pengukuran
beban tetap memakai `scripts/loadtest.py` (belum digabung jadi satu perintah),
jadi gerbang "satu perintah" belum lulus. Tes regresi yang ada:
`TestWorkspaceShrinkHysteresis`, `TestWorkspaceMaxSlotsBudget`,
`TestWorkspaceStatsConcurrent` (gagal di `-race` bila `Stats` membaca tabel
tanpa atomik, sudah dibuktikan), `TestMetricsText`, `TestMaxRouteFastReject`.

### W1. Memori workspace (dampak terbesar)

**Masalah.** 12 tabel (region x `-conc`), masing-masing tumbuh 12 B/slot tanpa
batas efektif (384 MiB/slot) dan tidak pernah mengecil. Beban campuran
menaikkan container ke 629 MiB, padahal halaman graf hanya ~45 MB.

**Rencana, berurutan.**
1. **Pool bersama lintas region.** Workspace tidak menyimpan referensi graf;
   `nodeBits` hanya bergantung pada `EdgeCount`. Pindahkan pool ke level server
   dan set `nodeBits/mask` saat `reset(g)`. Efek: tabel = `-conc`, bukan
   region x `-conc` (-3x pada deploy 3 region).
2. **Anggaran eksplisit.** Flag `-workspace-mb` (total). `maxSlots` per slot
   diturunkan dari anggaran / jumlah slot. Saat tabel mentok: klien menerima
   `422 search space exhausted` dengan pesan yang menyebut anggaran, bukan
   OOM. Relaxed ladder tetap dipakai lebih dulu (sudah ada).
3. **Mulai kecil + shrink.** Slot awal 64 K (0,75 MB) tumbuh sesuai kebutuhan;
   setelah query, bila `cap > 8 x used`, rehash turun ke 2x `used`. Hysteresis
   agar tidak rehash tiap request.
4. **Konkurensi total.** `-conc` menjadi batas total lintas region (semantik
   saat ini tidak terlihat dan mengalikan memori).

**Lulus bila.** 3 region, beban 600 pasangan campuran, `-conc 4`:
RSS <= 256 MB (dari ~629 MiB), tanpa perubahan satu pun jarak rute pada 332
pasangan E1, tidak ada 500/OOM; rute <=50 km tetap 332/332 cocok.

**Status (2026-11-08): dikerjakan W1.1-W1.4.** Detail angka di
`paper/RESULTS.md` §17.

- W1.1 pool bersama: `graph.WorkspacePool` di level server, satu pool `-conc`
  slot yang di-*rebind* per graf (`resetForGraph` menghitung ulang
  `nodeBits/genBits`, tabel dikosongkan bila berubah). Router kini memegang
  `wp *WorkspacePool`; `NewRouter` tetap ada untuk pemakai library
  (`rid-route`, `rid-bench`) dan membuat poolnya sendiri.
- W1.2 anggaran: flag `-workspace-mb` dipecah rata ke `-conc` slot (`12 B`/slot).
  Default `0` = tanpa anggaran eksplisit (plafon lama `maxSlots` 33 M
  slot = 384 MB/slot), bukan 256 MB/slot seperti draf: angka itu perlu
  pengukuran deploy dulu, dan default yang lebih kecil bisa menukar akurasi
  dengan 422 pada rute sah. Pemakaian dilaporkan di
  `routeid_workspace_bytes`.
- W1.3 mulai kecil + shrink: 65.536 slot (0,75 MB) awal; setelah query, bila
  `cap > 8 x used`, rehash turun ke `max(65.536, 2 x used)`. Histeresis 8x
  terbukti cukup: 600 pasangan campuran memicu 84-86 shrink dan 200-229 grow,
  wall-clock identik dengan build lama (172,2 vs 174,3 s, §17b).
- W1.4 konkurensi total: `-conc` sekarang berarti total lintas region, sesuai
  teks rencana; help text-nya diperbarui.
- A/B di mesin uji (graf yang sama dimuat 3x, 600 pasangan identik):
  anon 322 -> **213 MB** (`-conc 2`) dan 555 -> **425 MB** (`-conc 4`);
  RSS ikut turun 2004 -> 1895 MB dan 2236 -> 2107 MB (sisa RSS adalah halaman
  `mmap` 3 x 693 MB yang tidak dibandingkan). Hasil rute identik: 519 x 200,
  81 x 422 di keempat run.
- Gerbang "RSS <= 256 MB di deploy 3 region" tidak dinyatakan lulus dalam
  arti RSS: di homelab (Java + Sumatra + Nusa-Tenggara, `-conc 4`, 600 pasang,
  `GOMEMLIMIT=1GiB`, `-max-route-km 40`) terukur `anon` **311 MB**, tabel
  **201 MB** dari anggaran 256 MB, sedangkan RSS 958 MB karena `Rss`/`Pss`
  memuat halaman `mmap` graf yang tersentuh (1,21 GB berkas). A/B di box yang
  sama pada beban identik tanpa batas: `anon` 462 -> 415 MB, RSS 1.138 ->
  1.087 MB. Delta lebih kecil dari mesin uji karena di deploy hanya region Java
  yang menghasilkan tabel besar, dan jumlahnya sama-sama `-conc`; pool bersama
  menghapus pengganda region, bukan pengganda `-conc`. Angka lengkap:
  `paper/RESULTS.md` §17e.

### W2. Akurasi B: kejar p95 21% -> <=12%

**Masalah.** Ekor selisih jarak E1 berasal dari perbedaan fungsi biaya (A*
mengoptimalkan waktu, OSRM juga), bukan restriction yang di-skip (§14). Karena
itu perbaikan harus menyentuh model biaya, bukan menambah penalti belok
(§15 sudah membuktikan p95 tidak turun).

**Rencana.**
1. Audit kelas jalan: bandingkan `maxspeed` OSM vs tabel kecepatan per kelas
   pada 200 ruas acak (sampling deterministik). Perbaiki tabel hanya dengan
   bukti hitungan.
2. Uji varian biaya pada 332 pasangan E1: waktu murni, jarak murni, campuran
   (mis. 0,7 waktu + 0,3 jarak). Pilih yang menurunkan p95 tanpa menaikkan
   median |Δ| dan tanpa menaikkan durasi >2%.
3. Audit top-20 outlier p95: cek manual apakah routeid atau OSRM yang salah
   (ferry, turn restriction, snap). Setiap outlier masuk daftar uji regresi.
4. Bila 1-3 tidak mencapai 12%: engine kedua (Valhalla atau GraphHopper, OSM
   yang sama) untuk triangulasi, bukan untuk "menang"; tujuannya memisahkan siapa
   yang menyimpang.

**Lulus bila.** p95 |Δ| <= 12% pada 332 pasangan E1, median <= 0,1%, dan
20 outlier teratas punya penjelasan tertulis (siapa yang salah dan kenapa).

**Status (2026-11-08): belum dikerjakan.** Yang selesai di putaran ini hanya
sisi *kontrak* kelas rute, bukan model biaya: `-max-route-km` (dulu default 40,
kini 100 sejak 2026-11-12) menolak lebih awal dan membuat segmentasi <= 40 km
eksplisit, dan pada segmen itu p95 sudah 7,44% (§16c). Tabel kecepatan
(`internal/extract/extract.go:111-168`) tidak diubah: itu mengubah
`ProfileHash()` dan menuntut rebuild graf 3 region + E1 ulang, jadi harus
menjadi commit sendiri dengan diff statistik E1 yang dilampirkan.

### W3. Akurasi C: ground truth minimal (E2-lite)

**Masalah.** Tanpa ground truth, "akurasi" hanya kemiripan antar-engine.

**Rencana (pilih satu, keputusan pemilik proyek).**
- **(a) Trace GPS** 50-100 perjalanan nyata (log sendiri/relawan), bandingkan
  jarak & bentuk rute. Paling jujur, butuh pengumpulan data.
- **(b) Audit manual** 100 rute lintas kelas jarak dengan citra satelit +
  catatan OSM; label benar/salah + alasan. Murah, subjektif, cukup untuk
  mengukur presisi.
- **(c) API komersial** sampel kecil (mis. 200 rute) sebagai referensi ketiga;
  cepat, tapi menambah ketergantungan dan tidak bisa dipublikasikan penuh.

**Lulus bila.** Ada set uji tetap di repo (koordinat + label + sumber), skrip
penilaian, dan angka presisi yang bisa direproduksi.

### W4. Latensi (tanpa menukar akurasi)

**Masalah.** p50 30-50 km 139 ms, 148 km ~2,9 s; tanpa CH/MLD rute jauh mahal.

**Rencana.**
1. Eksperimen **bidirectional A*** di branch terpisah, target ekspansi turun
   >=40% pada 332 pasangan E1 dengan jarak rute identik (toleransi 0).
2. Bila gagal: **dekomposisi dua tingkat** untuk kelas rute panjang (sesuai
   non-goal "CH/MLD penuh" di `ROADMAP.md`), artefak terpisah, dengan gerbang:
   ukuran artefak <= 2x, RSS <= +50 MB, akurasi tidak berubah.
3. Optimisasi mikro hanya setelah profil: encoding polyline/geojson, alokasi
   per request, `reset()` tabel saat generasi habis.

**Lulus bila.** p50 30-50 km <= 100 ms, 148 km <= 1,5 s, p95 tidak naik, jarak
rute identik pada set regresi, RSS tidak naik.

**Status (2026-11-08): W4.3 sebagian, W4.1/W4.2 belum.** Yang terpasang hanya
scratch buffer path per workspace (`nodeBuf`/`edgeBuf`, dipotong bila
`cap > 64 K`) supaya rekonstruksi rute tidak mengalokasi slice baru tiap
request. Bidirectional A* belum dieksperimen, jadi target p50/p95 di atas belum
diklaim.

**Keputusan 2026-11-10: bidirectional A* dan ALT ditunda ke M7.** Pada kelas
yang dilayani (<=40 km) p95 E1 246 ms masih di bawah target 400 ms. Biaya
strukturnya tidak muat: reverse CSR Java 278 MB dan ALT 4 landmark 182 MB,
keduanya menekan anggaran RSS anonim 700 MB sebelum workspace. Eksperimen hanya
dibuka bila p95 42 km >400 ms atau batas rute naik di atas 50 km. Tidak ada kode
yang diubah.

## 4. Urutan & gerbang

| fase | isi | gerbang keluar |
|---|---|---|
| 0 | W0 metrik + harness + tes regresi | angka memori bisa dibandingkan antar commit |
| 1 | W1.1 pool bersama + W1.3 shrink | RSS beban turun >=40%, 332/332 jarak identik |
| 2 | W1.2 anggaran + W1.4 konkurensi | tidak ada alokasi > anggaran; 422 terjelaskan |
| 3 | W2 akurasi B | p95 <=12% atau keputusan eksplisit pindah ke (4) |
| 4 | W2.4 triangulasi / W3 ground truth | ada angka presisi yang bisa dikutip |
| 5 | W4 latensi | target p50/p95 di atas |

Fase 0-2 tidak boleh mengubah satu pun jarak rute: setiap PR wajib menjalankan
`paper/results/pairs-java.csv` dan melampirkan diff statistik (median, p95,
jumlah identik).

## 5. Non-goals rencana ini

- Tidak mengejar "lebih cepat dari OSRM": targetnya resource dan akurasi.
- Tidak mengganti format graph v4 di fase mana pun; CH (kalau jadi) adalah
  artefak tambahan.
- Tidak menambah traffic/ETA real-time.
- Tidak menjanjikan angka sebelum diukur dengan harness W0.

## 6. Risiko

| risiko | mitigasi |
|---|---|
| Pool bersama membuat `nodeBits` salah antar region | set bits dari graf saat reset; tes unit lintas ukuran graf |
| Anggaran terlalu kecil -> 422 pada rute sah | default anggaran diturunkan dari rute terpanjang yang didukung (<=50 km), flag untuk long-haul |
| Shrink menambah latensi (rehash) | hysteresis 8x; ukur p95 sebelum/sesudah, batalkan bila p95 naik |
| Perubahan biaya W2 mengubah rute yang sudah dipublikasikan | jalankan ulang E1 + simpan CSV lama; laporkan delta, bukan menimpa |
| CH menambah memori melebihi target | graph CH terpisah, dimuat hanya bila flag aktif |
