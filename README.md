# routeid

Router jalan raya Indonesia untuk app Flutter (polyline di Google Maps).
Bukan OSRM: graph binary hasil ekstraksi OSM + weighted A*, di-`mmap`.

## Kenapa

Kebutuhan: 2 titik lat/lng, rute <=50 km, mobil, Indonesia, akurat, resource
ringan, jalan tanpa bayar per-request.

Angka routeid di bawah **semua terukur di repo ini** (perintahnya ada di
`paper/RESULTS.md`). E1 sudah dijalankan: OSRM v5.26.0 MLD dan routeid di
**mesin yang sama** (Ryzen 5 5600G), PBF Java yang sama, 600 pasangan identik;
332 pasang masuk statistik jarak setelah filter snap (rincian `RESULTS.md` §13).
Angka latency lama (i5-7360U) tetap ada di §1-§12 dan tidak dicampur ke tabel ini.

| | routeid (Java, E1) | OSRM MLD v5.26.0 (Java, E1) |
|---|---|---|
| Graph di disk | 693 MB (1 file) | 5,08 GB (4,73 GiB, banyak file) |
| RSS sebelum query | 17 MB | 2,36 GiB |
| RSS setelah 600 pasangan | 815 MB | 2,45 GiB |
| Preprocess (22,7 jt node) | 28 s (`GOMEMLIMIT=2GiB`) | 250 s (151+83+16 s; peak 6,25 GB) |
| p50 0-5 / 5-15 / 15-30 / 30-50 / 50-80 km | 3,6 / 19,7 / 54,7 / 139,0 / 397,3 ms | 0,7 / 1,1 / 1,7 / 2,2 / 3,2 ms |
| p95 bucket yang sama | 14,0 / 68,7 / 226,7 / 478,4 / 1.060,9 ms | 1,1 / 1,7 / 2,3 / 3,0 / 4,5 ms |
| Selisih jarak rute (332 pasang) | median −0,005%, p95 21,05%, max 48,30% | referensi; gerbang p95 <12% gagal, bukan gerbang akurasi |
| Cakupan 600 pasangan | 332 cocok, 81 `422`, 187 keluar filter snap | 0 error; pada 81 itu snap tujuan p50 14,7 km |
| Turn restriction | 3.790 ban / 593 di-skip saat build | `car.lua` |
| Traffic / ETA real-time | tidak | tidak |

Kolom OSRM CH belum dijalankan. Binary/image routeid (6,6 MB statis, image
23 MB) terukur di build lama, bukan bagian E1.

Kolom yang paling penting untuk app: **rute <=50 km selesai 0,004-0,14 s (p50)
dan prosesnya memakai 17 MB saat idle, bukan 2,4 GiB**. Trade-off yang diterima:
tanpa CH/MLD rute jauh melambat (148 km = 2,7-4,2 s di v3/v4; p95 bucket 50-80 km
E1 = 1,06 s), jadi batas 40 km di app justru menjaga angka ini tetap bagus.
OSRM tetap ~25x lebih cepat di p50; keunggulan routeid ada di memori, artefak,
dan preprocess.

### Keunggulan yang bisa dipertahankan

1. **Footprint kecil & deterministik.** Idle **12,8 MB** dengan Java + Sumatra
   termuat (v4; **17 MB** untuk Java saja di E1), image 24 MB, start sampai
   `/healthz` 200 dalam 10 ms; OSRM idle 2,36 GiB. Cocok untuk homelab atau
   sidecar; setelah 600 query campuran di E1 RSS naik ke 815 MB, jadi box
   1 GB terlalu mepet untuk beban itu. Kenaikan itu bukan halaman graf:
   tabel workspace 12 B/slot tumbuh mengikuti edge unik yang dikunjungi rute
   terpanjang dan tidak mengecil sampai proses restart (lihat butir 4).
2. **Pipeline sesederhana tuntutan:** satu binary statis (6,6 MB), tanpa
   dependency runtime, tanpa profile Lua. `osm.pbf -> map.graph` 28 s untuk
   Jawa (E1) vs 250 s pipeline OSRM, jadi rebuild bulanan itu sepele.
3. **Multi-region satu proses.** Java + Sumatra + Bali/Nusra termuat bersama,
   region dipilih dari snap terdekat (bukan dari bbox request), dan dua file yang
   tumpang tindih diresnap ke satu file yang memuat kedua ujungnya.
4. **Idle kecil karena graf `mmap` belum tersentuh; biaya sebenarnya tabel
   pencarian.** Graph 1,06 GB (Java 693 MB + Sumatra 364 MB) di-`mmap`, jadi
   idle 12,8 MB dan puncak 399 MB pada 4 rute 148 km paralel (`-conc 4`),
   815 MB setelah 600 pasangan campuran di E1, sementara OSRM 2,45 GiB
   untuk beban yang sama. Pengukuran ulang (3 region, `-conc 4`): halaman
   file/mmap yang resident hanya ~45 MB; sisa RSS adalah tabel workspace
   `keys`/`gs`/`came` 12 B/slot. Sejak pool bersama + shrink, tabel = `-conc`
   (bukan region × `-conc`) dan mengecil sendiri: A/B 600 pasangan yang sama,
   graf Java dimuat 3x, `-conc 2` (`paper/RESULTS.md` §17) turun dari 322 MB menjadi
   213 MB anon (−34%) dengan 519 rute identik. Frontier heap A* cuma ~25
   ribu item.
5. **Turn restriction yang bisa diaudit.** Ban/only rule dihitung saat ekstraksi
   dan dilaporkan di `/healthz`; fallback (larangan dilepas agar rute ketemu)
   hanya 3,17% pada data uji.
6. **Dua optimisasi yang terbukti:** Hilbert ordering memangkas RSS
   metropolitan 1.225 -> 675 MB (1,8x) dan p50 7-20% lebih cepat; weighted A*
   `eps=1.5` 28% lebih cepat dari `eps=1.0` dengan 526/526 panjang rute identik.
7. **Kapasitas terukur per kelas rute** (i3-6100T, `-conc 4`): 74,6 RPS @7 km,
   17 RPS @15 km, dan 42 km 13,8-16,0 RPS pada pengukuran v4 terakhir
   (sebelumnya 11 RPS), saturasi di 4 query paralel. Untuk campuran pasangan
   <= 40 km (batas `-max-route-km`): p50 24,3 ms `-conc 1` dan plafon
   23,5-24,9 RPS (`paper/RESULTS.md` §16). Ini kapasitas routeid
   sendiri; E1 menunjukkan OSRM ~25x lebih cepat p50 untuk satu query.

### Batasan (jangan diklaim lebih)

- Tanpa hierarki (CH/MLD): >60 km melambat superlinear. 148 km = 2,7 s (v4;
  4,2 s di v3), dan koridor >800 km masih menabrak batas ekspansi (`ErrTooFar`).
- Satu graf = satu profil: mobil dan motor adalah dua artefak terpisah
  (`data/java.graph` vs `data/java-motor.graph`), bukan parameter query.
- Satu region per rute. Lintas selat dilayani hanya kalau kedua ujung jatuh di
  satu graph (mis. ferry Ketapang-Gilimanuk di `java.graph`); rute antar-file
  (Merak -> Bakauheni dengan `java.graph` + `sumatra.graph`) masih `422`.
- Restriction per kelas kendaraan diterapkan (`restriction:motorcar`/`motor_vehicle` untuk car, `motorcycle`/`motor_vehicle` untuk motor); 167 `restriction:conditional` (jam sekolah/pasar) tetap di-skip karena graf statis.
- **E1 sudah dijalankan; E2 belum.** E1 membandingkan OSRM MLD di mesin yang
  sama: median selisih jarak −0,005% (lulus gerbang <5%) tetapi p95 21,05%
  (gerbang <12% gagal), dan OSRM ~25x lebih cepat p50. Audit (`paper/RESULTS.md`
  §14) menunjukkan p95 jarak bukan akibat restriction yang di-skip,
  melainkan perbedaan fungsi biaya: routeid meminimalkan waktu murni (termasuk
  `track`), OSRM meminimalkan `weight` (durasi + penalti belok + preferensi
  kelas jalan). Penalti belok sudah dikirim (default `-turn-penalty 0.25`,
  tabel 2/7,5/20 s dibagi empat) dan diukur ulang pada 0/0,25/1/2x: p95 jarak
  tetap 21,12%, mean 3,60% -> 3,50%, p90 11,20% -> 10,56% (`paper/RESULTS.md`
  §15). Sisa selisih berasal dari tabel kecepatan kelas. Jadi README ini tidak
  mengklaim "tercepat" maupun "paling akurat"; yang terukur baru biaya per
  rute dan perbandingan antar-engine.
  Ground truth independen (E2) masih terbuka; resepnya di `docs/M6.md`.
- Tanpa auth, rate limit, atau TLS di server: taruh di belakang reverse proxy.

## Roadmap

North star proyek ini: **alternatif OSRM / Google Routes yang paling ringan**,
puluhan MB dan tetap <0,5 s untuk rute <=50 km di perangkat
murah. Budget-nya mengikat: RSS idle <= 20 MB, image <= 25 MB,
p95 42 km <= 400 ms, densitas >= 45 RPS/GB.

| milestone | isi | selesai bila |
|---|---|---|
| M1 | metadata graph, build info, golden test | `/healthz` menyebut pbf asal; regresi rute ketahuan otomatis |
| M2 | snap ke ruas (bukan node) | galat ujung p50 <= 20 m, p95 <= 60 m |
| M3 | biaya per ekspansi (heuristik, workspace 12 B/slot, heap 4-ary); ALT ditunda karena memori | latensi 1,9-2,0x, dalam box uji; RPS i3 menyusul deploy |
| M4 | timeout, rate limit, `/metrics`, ETag | tak ada request menahan slot >10 s |
| M5 ✅ | format v4 (Java 693 MB), ferry, profil motor, tangga ε, 7 region | rute lintas pulau `200` dengan penanda ferry |
| M6 🟡 | baseline OSRM (E1) selesai; ground truth (E2) terbuka | tiap klaim di README punya angka pendukung |

Rincian item, kriteria lulus, dan biaya: [`docs/ROADMAP.md`](docs/ROADMAP.md) dan
[`docs/BACKLOG.md`](docs/BACKLOG.md). Non-goals yang sengaja ditolak (traffic
real-time, cakupan dunia, CH/MLD penuh) juga tertulis di sana.

## Arsitektur

```
osm.pbf ──rid-extract──> map.graph (mmap) ──rid-server──> HTTP JSON/polyline
```

- **Node = setiap node OSM** → geometri jalan persis, tanpa shape point terpisah.
- **CSR** adjacency (Off/To/Len/Cls), koordinat fixed point 1e-7. Format v4:
  `Off` uint32, `Len` uint16 satuan 4 m, waktu tidak disimpan, dihitung
  dari `Len` + kelas kecepatan per edge (`Cls` = km/h x 2), dan ferry disimpan
  sebagai daftar edge sparse (bukan array penuh). 12 B/node + 7 B/edge.
- **Hilbert curve ordering**: node diurutkan geografis → A* tetap page-local
  (RSS metropolitan 1.225 → 675 MB, p50 7-20% lebih cepat).
- **Weighted A*** (ε=1.5 default): 28% lebih cepat dari ε=1.0, panjang rute identik.
- **Snap ke ruas**, bukan ke node: titik diproyeksikan tegak lurus ke segmen,
  lalu jarak/geometri dikoreksi (trim kalau rute keluar lewat ruas itu, spur kalau
  tidak). Grid 0.02° (~2,2 km/cell) dengan pencarian per-ring.
- Bobot = waktu tempuh (detik desimal) dari `maxspeed` atau default per kelas
  jalan yang disetel untuk Indonesia (residential 25 km/h, service 15 km/h, dll).
- **Profil** dipilih saat build: `car` (default, tabel mobil) atau `motor`
  (tabel motor: service/gang lebih permisif, tol dibuang kecuali
  `motorcycle=yes`, `path`/`footway` dipakai kalau ditandai boleh motor).
  Untuk motor, `oneway:motorcycle=yes|no|-1` menimpa `oneway` dan
  `motorcycle=destination` adalah izin lemah; `surface`/`smoothness` hanya
  menggeser kecepatan kelas tambahan (`path`, `footway`, `cycleway`,
  `pedestrian`, `steps`). Tag `*:conditional` dihitung `rid-tagstats` tetapi
  tidak dievaluasi karena graph statis.
- **Ferry** (`route=ferry`) ikut jadi edge; waktu pakai tag `duration` kalau ada
  (Merak-Bakauheni 2 jam ≠ 30 km / 20 km/jam), dan respons menandai segmennya.

## Pakai

```bash
make build          # semua binary ke bin/
make fetch REGION=java
make java           # unduh + build data/java.graph (profil mobil)
make graph REGION=java PROFILE=motor   # -> data/java-motor.graph
make islands        # ketujuh region Geofabrik, profil mobil
make motor          # ketujuh region, profil motor

# cek rute
bin/rid-route -graph data/java.graph -from -6.1754,106.8272 -to -6.5950,106.8166

# server
bin/rid-server -graph data/java.graph -addr :8080 -conc 4
curl 'localhost:8080/route?from=-6.1754,106.8272&to=-6.5950,106.8166&format=polyline'
```

Tanpa `make`:
```bash
go build -o bin/ ./cmd/...
GOMEMLIMIT=2GiB bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java.graph
```

Response:
```json
{"distance_m":56451,"duration_s":2538.8,"polyline":"...",
 "snap_from_m":4,"snap_to_m":24,
 "snap_from":[3.595201,98.672160],"snap_to":[3.600212,98.485017],
 "trimmed":{"from":true,"to":true}}
```
`format=json` (default) mengembalikan `points: [[lat,lon],...]`.
Polyline: precision 5 (~1.1 m), decode di Dart standar.

**Geometri mulai dan berakhir di jalan** (titik proyeksi `snap_from`/`snap_to`),
bukan di pin mentah; tidak ada garis spur menyeberang bangunan. Pin user tetap
digambar dari koordinat yang dikirim klien. Kalau butuh perilaku lama (garis
menyambung pin ke jalan), tambahkan `&pin=1`.

Query params: `simplify=<meter>` (Douglas-Peucker), `format=polyline|json`,
`pin=0|1`, `restrictions=0|1`. `simplify` harus angka tidak negatif; nilai lain
(`abc`, `-5`, `NaN`, `Infinity`) dijawab `400` sebelum graph disentuh. Header
respons: `X-Route-Ms`, `X-Route-Expansions`, `X-Route-Region`, `X-Route-Trim`
(`none|from|to|both`).

### Endpoint

| endpoint | guna |
|---|---|
| `GET /route?from=lat,lng&to=lat,lng` | rute. Opsional: `format=json\|points\|geojson\|polyline`, `simplify=<m>`, `pin=1`, `restrictions=0` |
| `GET /healthz` | kesiapan + per region: node/edge, bbox, ban/only rule, dan provenance (`format`, `pbf_sha256`, `built_at`, `source`, `extractor`, `profile_sha256`) |
| `GET /version` | commit + waktu build binary (`-ldflags`), versi Go, OS/arch |
| `GET /metrics` | Prometheus: `route_requests_total{code}`, histogram `route_request_duration_seconds`, `route_inflight`, `route_expansions_total`, `route_fallback_total`, `route_snap_retry_total`, `route_rate_limited_total`, `route_canceled_total`, `route_errors_total{reason}`, `routeid_workspace_slots`, `routeid_workspace_bytes`, `routeid_workspace_grows_total`, `routeid_workspace_shrinks_total` |

**Dua profil lewat satu base URL.** Satu proses memuat satu profil (snap
terdekat membuat dua graf hampir identik saling menutupi), jadi compose
menjalankan dua server di loopback dan `rid-gateway` memegang port publik:
`?profile=mobil|motor` memilih upstream, tanpa parameter dilayani mobil.
Parameter itu dicopot sebelum diteruskan (upstream dan log-nya tidak perlu tahu),
nilai lain dijawab `400`, dan respons membawa `X-Route-Profile`. `/healthz`
tanpa parameter menjawab kedua profil sekaligus (`503` bila salah satu mati),
`/healthz?profile=motor` hanya satu. ETag tiap profil berbeda karena fingerprint
grafnya berbeda, jadi cache klien tidak bisa tertukar. Lihat `deploy/README.md`
bagian 1c.

`/healthz` dan `/version` menjawab "graph dan binary mana yang sedang jalan",
jadi pertanyaan pasang-ulang tidak perlu masuk ke container. Respons rute memuat
`snap_from`/`snap_to` (titik proyeksi di jalan), `snap_from_m`/`snap_to_m`
(offset meter), `trimmed`, `bbox`, `points_n`, dan kalau rute menyeberang:
`ferry_m`, `has_ferry`, `ferry_spans` (rentang indeks ke `points`/polyline yang
bisa digambar putus-putus); header `X-Route-Ms`, `X-Route-Expansions`,
`X-Route-Region`, `X-Route-Trim`.

**Snap cadangan saat ruas terdekat buntu.** Snap hanya melihat geometri, jadi
pin di taman, halaman berpagar, atau jalan buntu bisa mendarat di ujung yang
tidak mengarah ke mana pun dan A* langsung menjawab `422 no route`. Untuk itu
handler mencoba ulang dari ruas terdekat berikutnya: 32 kandidat, melebar ke 512
kalau semuanya buntu, dan kandidat awal yang hanya menjangkau <= 512 node
(termasuk dirinya) dilewati karena pencarian dari situ tidak akan pernah sampai.
Paling banyak 4 pasangan awal-tujuan dicari, sekali, hanya setelah percobaan
pertama gagal, jadi jalur normal tidak berubah. Keberhasilannya dihitung di
`route_snap_retry_total`. Sisi tujuan belum bisa diperiksa dengan cara yang sama
(graf hanya punya CSR maju), jadi pin tujuan di dalam pocket tetap `422`.

### Cache, batas waktu, batas laju (M4)

- **`ETag` + `Cache-Control`.** `max-age` diambil dari flag `-maxage` (default
  3600 detik, `0` mematikan header cache). Tag dibentuk dari
  fingerprint graph (`pbf_sha256`, node/edge, `snap`, `eps`, `turn-penalty`,
  commit) plus query yang sudah dinormalisasi (urutan parameter dan GET-vs-POST
  tidak mengubah tag). Kirim `If-None-Match` → `304` tanpa body: **p50 0,2 ms**
  vs 43 ms untuk rute 42 km, jadi app yang mengulang query saat rotasi layar
  nyaris tidak memakai slot. Cek `If-None-Match` jalan sebelum rate limiter,
  jadi `304` tidak memakai kuota; sebaliknya `ETag`/`Cache-Control` hanya
  dipasang pada `200` dan `304`. Respons error (`400`, `422`, `429`, `500`,
  `504`) tidak pernah membawa `ETag` dan memakai `Cache-Control: no-store`.
  Semua respons memasang `Vary: Accept-Encoding`; `Content-Encoding: gzip`
  hanya pada respons yang punya body (tidak pada `304`/`204`).
- **Deadline `-timeout` (default 10 s).** `context` diteruskan ke dalam A* dan
  dicek tiap 256 ekspansi; lewat batas → `504 {"error":"deadline exceeded"}` dan
  workspace langsung dilepas. Klien yang memutus koneksi juga membatalkan
  pencarian.
- **Rate limit `-rate`/`-burst` (default 15/menit per IP, burst 5)** → `429` +
  `Retry-After`. Di belakang reverse proxy pakai `-trust-proxy` supaya IP diambil
  dari `X-Forwarded-For`; tanpa flag itu header diabaikan (tidak bisa dipalsukan).
- **Batas jarak `-max-route-km` (default 40, `0` = nonaktif).** Dua penjagaan:
  jarak garis lurus (great-circle, batas bawah jarak tempuh) ditolak `422` tepat
  setelah validasi koordinat, sebelum ETag/rate-limit/snap/A*; lalu jarak
  tempuh hasil rute dicek lagi setelah `RouteSnapCtx` supaya rute pendek yang
  berbelok jauh tetap ditolak. Keduanya menaikkan `reason="too_far"` di
  `/metrics` dan batasnya ikut masuk fingerprint ETag.
- **Biaya belok `-turn-penalty` (default 0,25, `0` = waktu murni).** Biaya
  belok dibaca dari sudut antara arah datang dan arah keluar
  (22,5°/67,5°/135° → 2/7,5/20 s, dikali flag) dan masuk ke `duration_s`,
  `distance_m` tidak berubah. Pengaruhnya pada E1: p95 jarak tetap 21,12%,
  mean 3,60% → 3,50%, ETA +1,6% median (`paper/RESULTS.md` §15).
- **Bobot heuristik `-eps` (default 1,5, `1,0` = optimal).** Turun ke 1,2 di
  E1 menghasilkan rute identik dengan 20% ekspansi lebih banyak, jadi 1,5
  dipertahankan. `-eps`, `-turn-penalty`, dan `-max-route-km` semuanya masuk
  fingerprint ETag: restart dengan nilai berbeda membatalkan cache lama.
- **Pool workspace bersama `-conc` + anggaran `-workspace-mb`.** Tabel A*
  (12 B/slot: `keys`, `gs`, `came`) tidak lagi disimpan per region × `-conc`;
  satu pool sebesar `-conc` dipakai ulang lintas region dan di-*rebind* ke
  graf yang dilayani (`nodeBits`/`mask`/generasi dihitung ulang). Tabel
  dimulai dari 65.536 slot (0,75 MB) dan hanya tumbuh saat rute panjang
  membutuhkan; saat dikembalikan ke pool dan terisi < 1/8 kapasitas, tabel
  mengecil lagi (histeresis 8x) sehingga satu rute 500 km tidak menahan
  puluhan MB di semua slot. `-workspace-mb` (default `0` = tanpa anggaran
  eksplisit, plafon lama 384 MB/slot) membagi anggaran ke `-conc` slot;
  tembus anggaran → `422 {"error":"search space exhausted (route too long)"}`
  (lihat `routeid_workspace_bytes`), bukan OOM.
  `routeid_workspace_slots`/`_bytes` menunjukkan biaya nyata saat ini, `_grows_total`
  dan `_shrinks_total` menunjukkan berapa kali tabel tumbuh/mengecil.
- Semua angka di atas terlihat di `/metrics`, jadi tidak perlu log untuk tahu
  berapa ekspansi/fallback/penolakan yang terjadi.

## Integrasi Flutter

Server: `GET /route` (query string) atau `POST /route` (JSON), dua-duanya
menerima `"lat,lng"` maupun `{"lat":..,"lng":..}`. Respons berisi
`distance_m`, `duration_s`, `polyline` (precision 5), `bbox` (siap dipakai
`CameraUpdate.newLatLngBounds`), `points_n`, dan `snap_from`/`snap_to`.

Polyline sudah menyusur jalan sampai ujungnya: titik pertama/terakhir adalah
proyeksi ke ruas (`snap_from`/`snap_to`), bukan pin mentah. Gambar marker dari
koordinat asli yang lo kirim, garis dari `points`; hasilnya pin di lokasi user,
rute mulai di jalan. Kalau butuh garis menyambung pin (perilaku lama), kirim
`'pin': true`.

```dart
final res = await http.post(
  Uri.parse('$base/route'),
  headers: {'Content-Type': 'application/json'},
  body: jsonEncode({
    'from': {'lat': fromLat, 'lng': fromLng},
    'to':   {'lat': toLat,   'lng': toLng},
    'format': 'polyline',
  }),
);
final j = jsonDecode(res.body) as Map<String, dynamic>;
final pts = decodePolyline(j['polyline'] as String);
final km = (j['distance_m'] as num) / 1000;
final snapFrom = (j['snap_from'] as List).cast<num>(); // [lat, lng] di jalan

GoogleMap(
  polylines: {Polyline(polylineId: const PolylineId('r'), points: pts, width: 5)},
  initialCameraPosition: CameraPosition(target: pts.first, zoom: 14),
);

// precision-5 polyline decoder (1e-5 deg), tanpa dependency tambahan
List<LatLng> decodePolyline(String s) {
  final out = <LatLng>[];
  var i = 0, lat = 0, lng = 0;
  while (i < s.length) {
    var shift = 0, result = 0, b;
    do { b = s.codeUnitAt(i++) - 63; result |= (b & 0x1f) << shift; shift += 5; }
    while (b >= 0x20);
    lat += (result & 1) != 0 ? ~(result >> 1) : (result >> 1);
    shift = 0; result = 0;
    do { b = s.codeUnitAt(i++) - 63; result |= (b & 0x1f) << shift; shift += 5; }
    while (b >= 0x20);
    lng += (result & 1) != 0 ? ~(result >> 1) : (result >> 1);
    out.add(LatLng(lat / 1e5, lng / 1e5));
  }
  return out;
}
```

Catatan lapangan:
- **Emulator Android** tidak bisa `127.0.0.1`: pakai `10.0.2.2:8080`.
- **HP fisik** pakai IP LAN laptop (`ip addr`), pastikan `-addr :8080` (bukan
  `127.0.0.1`) dan firewall lokal mengizinkan.
- **flutter web** sudah aman: CORS `*` + preflight `OPTIONS` ditangani server.
- `simplify=<meter>` kalau payload terlalu besar (mis. `simplify=8`).
- Respons 422 dengan `{"error":...}` untuk kasus normal (di luar Indonesia,
  tidak ada jalan dalam radius snap, dua titik beda region). Tampilkan apa
  adanya saat development.
- Field `fallback: true` berarti larangan belok dilepas agar rute ketemu;
  berguna untuk telemetri, bukan untuk user.

### Coba tanpa app (Bruno / Hoppscotch / Postman / curl)

**Bruno** (native, environment ikut ter-import): buka folder
`docs/bruno/routeid/` (Open Collection), lalu pilih environment `Local` atau
`Homelab` di kanan atas. Isinya 26 request dengan test JavaScript Bruno
(81 blok `test`): Meta (`healthz` termasuk bentuk agregat gateway, `version`,
`metrics`, dan versi `profile=motor`), Route inti (points, geojson, polyline +
`polyline_b64`, POST objek/string, `restrictions=0`, ETag → 304, `profile=motor`
plus bukti ETag antar profil tidak bisa tertukar), kelas jarak (42 km, 148 km),
region lain (Sumatra, Bali), dan 5 kasus error (termasuk `profile=truk` → 400).
Request khusus gateway melewatkan pemeriksaan header kalau base URL langsung
menuju satu server, jadi keduanya tetap bisa dijalankan. Versi CLI:

```bash
cd docs/bruno/routeid
npx @usebruno/cli run -r --env Local
npx @usebruno/cli run -r --env Homelab --delay 4300   # rate limit 15/menit
```

**Postman / Hoppscotch**: collection + environment JSON (Bruno tidak bisa
impor file environment Postman, jadi pakai folder `docs/bruno/` di atas):

| file | isi |
|---|---|
| `docs/routeid.postman_collection.json` | 21 request inti (set sebelum gateway; koleksi Bruno di atas yang terbaru), tiap request punya test script Postman. |
| `docs/routeid.postman_environment-local.json` | `baseUrl = http://127.0.0.1:8080` |
| `docs/routeid.postman_environment-homelab.json` | `baseUrl = http://192.168.1.219:8080` |

Import collection lalu environment (Postman: File → Import; Hoppscotch:
Collections → Import → Postman), pilih environment di kanan atas. Tanpa
environment tetap jalan: default collection variable `http://127.0.0.1:8080`,
jadi tidak muncul `Invalid URL`.

Request di folder "Region lain" butuh graf region tsb: server lokal yang hanya
memuat `data/java.graph` menjawab 422 dan test menerima itu; homelab memuat
`java`, `sumatra`, `nusa-tenggara`. Homelab memakai `-rate 15` per menit
(burst 5) per IP, jadi jangan menekan Run seluruh koleksi berulang-ulang:
request identik dengan `If-None-Match` dibalas 304 dan tidak memakan kuota.
Di homelab kedua profil ada di satu base URL `http://192.168.1.219:8080`:
tambahkan `&profile=motor` untuk motor. Port 8081 tidak lagi dipakai klien;
upstream-nya hanya mendengarkan di loopback homelab (18080/18081).

Kalau dijalankan di emulator/HP, ganti `127.0.0.1` jadi `10.0.2.2` (emulator
Android) atau IP LAN mesin (`hostname -I`) lewat environment variable.

Atau manual di Hoppscotch:

| field | isi |
|---|---|
| Method | `GET` |
| URL | `http://127.0.0.1:8080/route?from=-6.2088,106.8456&to=-6.2297,106.8166&format=polyline` |

`http://127.0.0.1:8080` boleh dipanggil dari `hoppscotch.io` (HTTPS) karena
server mengirim `Access-Control-Allow-Private-Network: true`. Kalau browser
tetap memblokir, pakai Hoppscotch desktop, atau tunnel:
`cloudflared tunnel --url http://localhost:8080`.

### Cek hasil rute tanpa app

**Cara paling aman: pakai `points` (array), bukan `polyline`.**

```bash
curl -s "http://127.0.0.1:8080/route?from=-7.8435723,110.392939&to=-7.7393368,110.3760593" \
  > /tmp/r.json
scripts/route2geojson.py /tmp/r.json > /tmp/r.geojson
```
Ringkasan (jumlah titik, bbox, ujung, jarak) dicetak ke stderr; tempel isi
`/tmp/r.geojson` ke https://geojson.io untuk melihat garisnya di peta.

**Cara tercepat kalau cuma mau lihat garisnya:** minta `format=geojson`, lalu
copy isi field `geojson` dari respons ke https://geojson.io (panel JSON kiri)
atau simpan sebagai file `.geojson`. Tidak ada backslash, jadi tidak ada
decoder yang bisa salah.

```bash
curl -s "http://127.0.0.1:8080/route?from=-7.8435723,110.392939&to=-7.7393368,110.3760593&format=geojson"
```

Kalau file picker tidak menampilkan `/tmp`, tulis GeoJSON ke folder project
(`scripts/route2geojson.py r.json > r.geojson`) lalu buka dari situ.

**`polyline_b64`.** Karena charset polyline memuat `\`, respons
`format=polyline` juga menyertakan `polyline_b64` (base64 dari string yang
sama). Versi ini bebas backslash, jadi aman di-copy-paste. Decoder lokal yang
menerima JSON, base64, maupun polyline mentah (termasuk yang backslash-nya
sudah dobel):

```bash
curl -s ".../route?from=..&to=..&format=polyline" > rp.json
scripts/polyline-decode.py rp.json                 # ringkasan + cek points_n
scripts/polyline-decode.py rp.json > r.geojson     # siap tempel ke geojson.io
```

**Peringatan `polyline`:** encoding polyline Google memakai seluruh rentang
karakter printable, termasuk `\` (backslash). Di JSON karakter itu tampil
sebagai `\\`, dan banyak decoder (termasuk situs decoder online) berhenti
atau melompat di titik itu, garis jadi terpotong di tengah jalan tanpa error
apa pun. Polyline hanya ada kalau request memakai `format=polyline`, dan
jangan copy-paste teks JSON mentah; ekstrak lewat JSON parser:

```bash
curl -s "http://127.0.0.1:8080/route?from=-7.8435723,110.392939&to=-7.7393368,110.3760593&format=polyline" \
  > rp.json
python3 -c "import json;print(json.load(open('rp.json'))['polyline'])" > r.polyline
```

Smoke test sebelum nyentuh app:
```bash
make build && bin/rid-server -graph data/java.graph -addr :8080
scripts/smoke.sh http://127.0.0.1:8080
```

## Kapasitas (terukur)

Homelab uji: i3-6100T (2 core/4 thread), container 1,5 GiB, `-conc 4`.
Load test: `scripts/loadtest.py`.

| rute | server p50 (1 query) | RPS puncak | concurrency jenuh |
|---|---|---|---|
| 7 km (Palembang) | 47 ms | ~75 | 4 |
| 15 km (Yogyakarta) | 136 ms | ~17 | 4 |
| 148 km (Jakarta–Bandung) | 4,2 s | ~0,6 | 4 |

Kalau rute dibatasi maksimum 40 km (batas desain aplikasi), acuan yang dipakai
adalah baris 42 km (Jakarta -> Cikarang), bukan 148 km:

Angka sebelum M3 (baris 42 km Jakarta -> Cikarang, `-conc 4`): RPS puncak 10,9
dan server p50 326 ms. Sesudah M3, di box yang sama, rute 41,7 km dengan
`-conc 4` dan `scripts/loadtest.py`:

| | sebelum | sesudah |
|---|---|---|
| RPS | 15,8-21,7 | 36,1-40,1 |
| server p50 | 176-216 ms | 93-102 ms |
| p95 server | 208-330 ms | 105-145 ms |
| RSS beban (4x 148 km) | 767,6 MiB | 572,2 MiB |

Selisih RPS "sebelum" dengan baris tabel di atas berasal dari rute yang berbeda
(Jakarta -> Cikarang 40,7 km vs 41,7 km Surabaya); yang penting rasionya, diukur
bergantian pada mesin dan graph yang sama: **~2x RPS, 1,9x p50, -25% memori**
(lihat `paper/RESULTS.md` §10-10b).

Sesudah format v4 (M5) di box yang sama: ekspansi tiap rute identik, CPU per
query turun 1,6-2x, jadi RPS naik 1,4-1,5x dan p50 turun ~30 % (rasio A/B
bergantian: v3 9,97-10,58 RPS / p50 361-376 ms vs v4 13,79-16,02 RPS / p50
223-283 ms pada rute 42 km, `-conc 4`, `paper/RESULTS.md` §12b). Artifak graph
mengecil 32 % (Java 693 MB) dan RSS diam 64 MiB.

Dengan build terbaru (penalti belok 0,25x, tiga region termuat) diukur langsung
himpunan pasangan <= 40 km, bukan satu rute contoh: 298 dari 600 pasang
`pairs-java.csv` (jarak p50 12,6 km, p95 36,4 km), instance `-rate 0`,
`GOMEMLIMIT=768MiB`, i3-6100T:

| concurrency | RPS | p50 | p95 |
|---|---|---|---|
| 1 | n/a | 24,3 ms | 246,2 ms |
| 2 | 24,7 | 32,9 ms | 336,6 ms |
| 4 | 23,5 | 73,6 ms | 705,4 ms |
| 8 | 24,9 | 192,7 ms | 1.205,0 ms |

Plafon ~24-25 RPS di 2 core fisik; `-conc 2` memberi p50 sepertiga `-conc 4`
pada RPS yang sama, jadi untuk batas 40 km `-conc 2` cukup. Untuk segmen ini
akurasi E1 juga lulus: p95 selisih jarak 7,44% (median 0,03%) pada 324 pasang
OSRM <= 40 km. Rincian: `paper/RESULTS.md` §16.

Memori dengan build pool bersama (600 pasang `pairs-java.csv`, tiga region,
`-conc 4`, sesudah beban; `paper/RESULTS.md` §17e):

| build | beban | anon | RSS | tabel workspace |
|---|---|---|---|---|
| `f9693f8` (12 tabel) | 600 pasang, tanpa batas | 462 MB | 1.138 MB | tak terukur |
| `8e2dc11` (pool 4) | 600 pasang, tanpa batas | 415 MB | 1.087 MB | 126 MB |
| `8e2dc11` | 40 km (`-max-route-km 40`), `GOMEMLIMIT=1GiB` | 311 MB | 958 MB | 201 MB dari 256 MB |

RSS di atas menghitung halaman `mmap` graf yang tersentuh (Java+Sumatra+
Nusa-Tenggara = 1,21 GB berkas, `Pss` ~= `Rss`); bagian yang dikendalikan
aplikasi adalah `anon`, dan itu turun 10% pada beban yang sama serta 49%
dibanding `anon` 612 MB pada §5. Idle container sesudah beberapa rute:
RSS 21,6 MB, `anon` 6,7 MB, `routeid_workspace_bytes` 3,1 MB.

Sebelum M3, saturasi ada di ~11-12 RPS: pada `-conc 4` CPU terpakai ~3,5
core-ekuivalen (10,9 query x 0,33 s), jadi menambah container tidak menambah RPS
selama core fisiknya tetap 2. Sesudah M3 titik jenuhnya naik ke ~4 core lagi
(38,6 query x 0,1 s = 3,9 core-ekuivalen).

Catatan penting:

- `-conc 1` (default lama) membatasi ke ~7 RPS untuk rute 15 km; menaikkan
  `-conc` ke 4 menaikkan throughput ~2,4x dan konkurensi klien di atas 4 tidak
  menambah apa pun (sudah jenuh di 2 core fisik).
- RPS berbanding terbalik dengan panjang rute; A* naik superlinear.
- Antrean memperburuk latency, bukan throughput: pada c=4 rute 15 km, p50 klien
  226 ms sementara server 226 ms (murni antre).
- Satu query 148 km menempati 1 slot workspaces selama 4–7 s. Kalau app lo
  banyak query panjang, siapkan timeout klien ≥10 s dan rate limit per-IP.
- Memori puncak terukur saat 4x rute 148 km paralel: 745 MiB (di bawah limit
  1,5 GiB, tanpa OOM).

## Deploy (homelab)

Docker Compose dan systemd unit siap pakai ada di `deploy/`; lihat
[`deploy/README.md`](deploy/README.md) untuk langkah lengkap, batas memori,
dan catatan keamanan. Ringkasnya:

```bash
cd deploy && docker compose up -d --build     # atau deploy/routeid.service
scripts/smoke.sh http://127.0.0.1:8080
```

## Multi-region (per pulau)

Graph bisa dipecah per pulau lalu disajikan satu server; region dipilih dari
snap terdekat (bbox antar-extract saling tumpang tindih):

```bash
bin/rid-server -graph data/java.graph,data/sumatra.graph -addr :8080
bin/rid-server -graph data/ -addr :8080          # semua *.graph di direktori
```

Ketujuh region Geofabrik terbangun (format v4, profil mobil; disk = artefak):

| region | node | edge | disk | build (4 worker) |
|---|---|---|---|---|
| java | 22,7 M | 46,7 M | 693 MB | 3m53s |
| sumatra | 11,9 M | 24,3 M | 364 MB | 1m54s |
| kalimantan | 7,1 M | 14,2 M | 215 MB | 56s |
| sulawesi | 5,9 M | 11,7 M | 173 MB | 45s |
| nusa-tenggara (Bali-Nusra) | 5,2 M | 10,3 M | 153 MB | 51s |
| maluku | 0,8 M | 1,6 M | 25 MB | 8s |
| papua | 1,1 M | 2,2 M | 34 MB | 8s |
| nasional (7 region, satu file) | 53,9 M | 110,2 M | 1.650 MB | 8m08s |

Diukur di box 4 core yang dipakai bersama (4 worker, `GOMEMLIMIT=4GiB`), jadi
angka build ini lebih lambat daripada satu region sendirian di mesin sepi.
Profil motor membesar sedikit karena gang/`path` yang boleh dilalui motor ikut
masuk: `java-motor` 682 MB (23,4 M node / 48,2 M edge, 4m01s).

Cek sehat graph per region sebelum dipakai (snap per titik + peta lubang):

```bash
bin/rid-graphinfo -graph data/sumatra.graph                       # header + grid
bin/rid-graphinfo -graph data/sumatra.graph -at 3.5952,98.6722    # snap Medan
bin/rid-graphinfo -graph data/sumatra.graph \
  -scan 3.0,98.0,4.2,99.2,0.05                                    # rapat: lubang data
```

Uji yang sudah lolos di satu proses (java + sumatra): Jakarta->Bandung
(`java`, 148.5 km), Medan->Binjai (`sumatra`, 22.7 km), Medan->Parapat
(175.0 km), Banda Aceh->Medan (578.7 km), RSS total 357 MB (mmap).

**Rute lintas selat.** Ferry adalah edge biasa, jadi rute menyeberang keluar
kalau kedua ujung ada di satu graph:

| query | hasil |
|---|---|
| Ketapang -> Gilimanuk (`java.graph`) | 15,1 km, `ferry_m=4180`, `ferry_spans=[[321,329]]` |
| Merak -> Bakauheni (`indonesia.graph`) | 29,2 km / 1,45 jam, `ferry_m=28535` |
| Jakarta -> Medan (`indonesia.graph`) | 2004,5 km / 25,8 jam, `ferry_m=29072`, `relaxed=true` |

Kalau titiknya jatuh di dua file region berbeda, server tetap membalas `422`
("cross-region route not supported"), stitching antar-file ada di BACKLOG 17b.
Untuk melayani lintas pulau hari ini: bangun graph nasional
(`scripts/build-national.sh`, 8 menit, `GOMEMLIMIT=4GiB` di box 7 GB) atau
taruh banyak region di satu server dan terima batas itu.

**Koridor panjang = `relaxed`.** Sebelum menyerah dengan `ErrTooFar`, router
mencoba ulang dengan heuristik lebih rakus (ε 2,0 lalu 3,0) dan menandai hasil
`relaxed=true` + `epsilon`. Ongkosnya: Jakarta-Bandung ε 2,0 = 148,62 km
(+0,07%), ε 2,5 = 155,3 km (+4,4%). Jakarta -> Medan (2004 km) butuh ε 3,0, 4,8
jt ekspansi, 19-22 s, jadi pakai `-timeout 60s` untuk kelas ini, bukan default
10 s.

## Repo layout

```
cmd/rid-extract      pbf -> .graph (offline, bulanan)
cmd/rid-server       HTTP /route, /healthz, /version (prod)
cmd/rid-gateway      satu base URL, profil dipilih ?profile=mobil|motor
cmd/rid-route        CLI satu rute (debug)
cmd/rid-graphinfo    inspeksi header/grid + probe snap & scan lubang data
docs/ROADMAP.md      north star, budget, milestone
docs/BACKLOG.md      backlog teknis: masalah, rencana, kriteria lulus
cmd/rid-bench        benchmark latency/akurasi + baseline -no-restrictions
cmd/rid-restcheck    harness kepatuhan turn restriction
cmd/rid-restats      audit data relasi restriction di pbf
cmd/rid-tagstats     census tag way untuk profil motor (akses, maxspeed, surface)
cmd/rid-snapcheck    ukur galat snapping (node vs ruas)
internal/extract     parsing OSM, profil mobil, restriction, urutan Hilbert
internal/graph       format biner, mmap, snapping, A*
paper/               draf ACM + RESULTS.md (semua angka) + EXPERIMENTS.md
scripts/             fetch.sh, build-national.sh, smoke.sh, loadtest.py,
                     m6-osrm.sh + m6-compare.py (baseline OSRM, lihat docs/M6.md),
                     motor-ab.py (A/B mobil vs motor pada pasangan tetap)
```

## Test

```bash
make test                                              # go vet + go test ./...
go test ./internal/graph -run TestGoldenRoutes -update  # terima perubahan rute yang disengaja
```

Unit test mencakup round-trip format (termasuk Ban/Only), semantik
`only_*`, pembuktian router memilih jalan memutar 2 hop saat belokan terlarang,
round-trip provenance v3, snapping ruas (proyeksi, trim, spur), dan **golden test
48 pasangan node + 48 pasangan snap**: kota sintetis 30x30
ditulis ke disk, di-snap, dirutekan, lalu dibandingkan per pasangan
(`dist_m`, `time_ds`, jumlah node, hash FNV urutan node). Perubahan tak sengaja
pada profil, snapping, atau A* langsung tampak sebagai diff, sudah diverifikasi
tidak vakum: mengubah satu kecepatan 8,0 -> 8,5 m/s menggagalkan 3 pasangan.

CI (GitHub Actions) menjalankan vet + test + build di tiap push.

## Benchmark

```bash
bin/rid-bench -graph data/java.graph -n 120 -emit-pairs pairs.csv   # fix workload
bin/rid-bench -graph data/java.graph -pairs pairs.csv -eps 1.5      # percentile
```
Temuan utama (601 pasang tetap, Java):
- ε=1.5 → 28% lebih cepat dari ε=1.0, tanpa mengubah panjang rute (526/526 sama).
- Hilbert ordering → p50 7-20% lebih cepat, RSS metropolitan 1.2 GB → 675 MB.
- p50: 1.2 ms (≤5 km), 62 ms (38 km), 146 ms (63 km). p95 di 63 km: 472 ms.

## Paper

`paper/` berisi draf short paper (ACM `acmart`), `refs.bib` (DOI terverifikasi),
`RESULTS.md` (semua angka + cara reproduksi), `EXPERIMENTS.md` (checklist
kekurangan: ground truth Google, pembanding selain OSRM MLD).

## Turn restriction

A* berjalan di atas **edge**, bukan node (`internal/graph/astar.go`): state =
edge yang baru dilewati, sehingga pasangan (edge masuk, edge keluar) bisa
diperiksa terhadap larangan belok.

```bash
bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java.graph
#   restrictions: seen=4653 usable=4203 via_node=2808 via_way=1258 #                 ban=3911 only=155 skipped{...unresolved=67}
bin/rid-restcheck -graph data/java.graph        # uji kepatuhan
bin/rid-bench -graph data/java.graph -no-restrictions   # baseline A/B
```

Yang dimodelkan: `no_left_turn`, `no_right_turn`, `no_u_turn`,
`no_straight_on`, `no_entry`, `no_exit` → daftar pasangan terlarang;
`only_*` → daftar pasangan yang diizinkan (sisanya dilarang). Via-node dan
via-way (termasuk rantai beberapa via-way) didukung.

Yang tidak dimodelkan (dan dilaporkan di statistik): `restriction:conditional`
(berbasis waktu), `except=` untuk moda selain mobil, dan via-way yang tidak bisa
dirantai. Via-way dilarang secara konservatif (blokir masuk via-way dari arah
yang dibatasi); kalau ini membuat tujuan tak terjangkau, router otomatis ulang
tanpa restriction dan menandai `"fallback": true` di respons.

Biaya format: 8 B per pasangan. Jawa: ~4.1 k pasangan = **32 KB** (0,003% dari
graf). Bandingkan tabel CSR penuh: 8 B x 46,7 jt edge = 374 MB.

## Format file

Lihat doc comment `internal/graph/format.go`. Header 320 B + 14 section: Lat,
Lon (int32 1e-7), CSR `Off`/`To`, `Len` (uint16 satuan 4 m), `Cls` (uint8 = km/h
x 2), daftar edge ferry (sparse), grid snap, lalu `Ban`/`Only` (pasangan edge
terlarang/diizinkan). **Waktu tempuh tidak disimpan**: dihitung dari
`Len x 288/Cls` desisecond, jadi satu edge = 4 + 2 + 1 = 7 B dan satu node =
4 + 4 + 4 = 12 B.

Versi 4 menambah: `Off` uint32, `Len`/`Cls` di atas, dan daftar ferry. Header
punya provenance sejak v3 (SHA-256 pbf, waktu build) plus blob `key=value` di
akhir file (sumber, profil + hash, batas kecepatan, versi extractor, order,
grid). File v1-v3 ditolak oleh reader v4 (lebarnya beda, salah baca lebih
buruk daripada error jelas): jalankan ulang extractor, 1-8 menit per region.

## Lisensi

Kode: MIT (lihat `LICENSE`). Data: © OpenStreetMap contributors, ODbL,
wajib tampilkan atribusi "© OpenStreetMap contributors" di app, dan turunan
database (file `.graph`) tetap ODbL.
Update: ekstrak ulang bulanan lalu swap file + restart (74 s untuk Java).
