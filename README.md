# routeid

Router jalan raya Indonesia untuk app Flutter (polyline di Google Maps).
Bukan OSRM: graph binary hasil ekstraksi OSM + weighted A*, di-`mmap`.

## Kenapa

Kebutuhan: 2 titik lat/lng, rute ≤50 km, mobil, Indonesia, akurat, resource ringan.

| | routeid (Java) | OSRM MLD (Java) |
|---|---|---|
| Graph di disk | 1019 MB | ~1-2 GB |
| RSS saat start | **15 MB** | ~1-1.5 GB |
| RSS setelah 4× rute 50 km | **104 MB** | tetap 1-1.5 GB |
| Preprocess (22.7 M node, 46.7 M edge) | **74 s** | ~15-40 menit |
| Latency 6 km | 13 ms | 10-30 ms |
| Latency 25 km | 120-180 ms | 10-30 ms |
| Latency 50 km | 95-160 ms | 10-30 ms |
| Turn restriction | ya (edge-based A*) | ya |
| Traffic / ETA real-time | tidak | tidak |

Trade-off yang diterima: rute jauh (>60 km) melambat karena tanpa CH/MLD.
Untuk ≤50 km + cache, hasilnya cukup.

## Arsitektur

```
osm.pbf ──rid-extract──> map.graph (mmap) ──rid-server──> HTTP JSON/polyline
```

- **Node = setiap node OSM** → geometri jalan persis, tanpa shape point terpisah.
- **CSR** adjacency (Off/To/Len/Time), koordinat fixed point 1e-7.
- **Hilbert curve ordering**: node diurutkan geografis → A* tetap page-local
  (RSS turun dari 579 MB ke 104 MB, latency ~1.5-2× lebih cepat).
- **Weighted A*** (ε=1.5 default): rute sama dengan ε=1.0 tapi 2.4× lebih cepat.
- **Grid snapping** 0.02° (~2.2 km/cell) dengan pencarian per-ring.
- Bobot = waktu tempuh (detik desimal) dari `maxspeed` atau default per kelas
  jalan yang disetel untuk Indonesia (residential 25 km/h, service 15 km/h, dll).

## Pakai

```bash
make build          # semua binary ke bin/
make fetch REGION=java
make java           # unduh + build data/java.graph

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
{"distance_m":56451,"duration_s":2538.8,"polyline":"...","snap_from_m":311,"snap_to_m":61}
```
`format=json` (default) mengembalikan `points: [[lat,lon],...]`.
Polyline: precision **5** (~1.1 m), decode di Dart standar.

Query params: `simplify=<meter>` (Douglas-Peucker), `format=polyline|json`.

## Integrasi Flutter

Server: `GET /route` (query string) atau `POST /route` (JSON) — dua-duanya
menerima `"lat,lng"` maupun `{"lat":..,"lng":..}`. Respons berisi
`distance_m`, `duration_s`, `polyline` (precision 5), `bbox` (siap dipakai
`CameraUpdate.newLatLngBounds`) dan `points_n`.

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
- **Emulator Android** tidak bisa `127.0.0.1` — pakai `10.0.2.2:8080`.
- **HP fisik** pakai IP LAN laptop (`ip addr`), pastikan `-addr :8080` (bukan
  `127.0.0.1`) dan firewall lokal mengizinkan.
- **flutter web** sudah aman: CORS `*` + preflight `OPTIONS` ditangani server.
- `simplify=<meter>` kalau payload terlalu besar (mis. `simplify=8`).
- Respons 422 dengan `{"error":...}` untuk kasus normal (di luar Indonesia,
  tidak ada jalan dalam radius snap, dua titik beda region). Tampilkan apa
  adanya saat development.
- Field `fallback: true` berarti larangan belok dilepas agar rute ketemu —
  berguna untuk telemetri, bukan untuk user.

### Coba tanpa app (Hoppscotch / Postman / curl)

Import `docs/routeid.postman_collection.json` di Hoppscotch
(Collections → Import → Postman) atau Postman (File → Import). Isinya 7 request
siap tekan, **tanpa variabel** (URL absolut `http://127.0.0.1:8080`) supaya
tidak gagal `Invalid URL` gara-gara environment tidak ikut ter-import:
`healthz`, GET polyline, GET `points`, POST objek, POST string, perbandingan
`restrictions=0`, dan satu kasus 422.

Kalau dijalankan di emulator/HP, ganti `127.0.0.1` jadi `10.0.2.2` (emulator
Android) atau IP LAN mesin (`hostname -I`).

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
atau melompat di titik itu — garis jadi terpotong di tengah jalan tanpa error
apa pun. Polyline hanya ada kalau request memakai `format=polyline`, dan
jangan copy-paste teks JSON mentah — ekstrak lewat JSON parser:

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

## Multi-region (per pulau)

Graph bisa dipecah per pulau lalu disajikan satu server; region dipilih dari
snap terdekat (bbox antar-extract saling tumpang tindih):

```bash
bin/rid-server -graph data/java.graph,data/sumatra.graph -addr :8080
bin/rid-server -graph data/ -addr :8080          # semua *.graph di direktori
```

Terukur (VmHWM):
| region | node | edge | disk | build | peak RAM build |
|---|---|---|---|---|---|
| java | 22.7 M | 46.7 M | 1.019 MB | 86 s | 3.2 GB (1.8 GB dgn `GOMEMLIMIT=1800MiB`) |
| sumatra | 11.9 M | 24.3 M | 533 MB | 23 s | 1.4 GB |
| nasional (est.) | ~44 M | ~90 M | ~2.0-2.2 GB | ~3 mnt | ~5 GB dgn `GOMEMLIMIT=5GiB` |

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

Rute lintas-region (mis. Merak -> Bakauheni) belum didukung: dua titik harus
jatuh di satu region, kalau tidak server membalas `422`. Kalau butuh, gabungkan
jadi satu graph dari pbf nasional (`scripts/build-national.sh`) — butuh mesin
≥8 GB atau streaming extractor yang belum ada.

## Repo layout

```
cmd/rid-extract      pbf -> .graph (offline, bulanan)
cmd/rid-server       HTTP /route, /healthz (prod)
cmd/rid-route        CLI satu rute (debug)
cmd/rid-graphinfo    inspeksi header/grid + probe snap & scan lubang data
cmd/rid-bench        benchmark latency/akurasi + baseline -no-restrictions
cmd/rid-restcheck    harness kepatuhan turn restriction
cmd/rid-restats      audit data relasi restriction di pbf
cmd/rid-snapcheck    ukur galat snapping (node vs ruas)
internal/extract     parsing OSM, profil mobil, restriction, urutan Hilbert
internal/graph       format biner, mmap, snapping, A*
paper/               draf ACM + RESULTS.md (semua angka) + EXPERIMENTS.md
scripts/             fetch.sh, build-national.sh, smoke.sh
```

## Test

```bash
make test     # go vet + go test ./...
```

Unit test mencakup round-trip format (termasuk Ban/Only), semantik
`only_*`, dan pembuktian router memilih jalan memutar 2 hop saat belokan
terlarang. CI (GitHub Actions) menjalankan vet + test + build di tiap push.

## Benchmark

```bash
bin/rid-bench -graph data/java.graph -n 120 -emit-pairs pairs.csv   # fix workload
bin/rid-bench -graph data/java.graph -pairs pairs.csv -eps 1.5      # percentile
```
Temuan utama (601 pasang tetap, Java):
- ε=1.5 → 28% lebih cepat dari ε=1.0, **tanpa** mengubah panjang rute (526/526 sama).
- Hilbert ordering → p50 7-20% lebih cepat, RSS metropolitan 1.2 GB → 675 MB.
- p50: 1.2 ms (≤5 km), 62 ms (38 km), 146 ms (63 km). p95 di 63 km: 472 ms.

## Paper

`paper/` berisi draf short paper (ACM `acmart`), `refs.bib` (DOI terverifikasi),
`RESULTS.md` (semua angka + cara reproduksi), `EXPERIMENTS.md` (checklist
kekurangan: baseline OSRM, ground truth Google, turn restriction, build nasional).

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

Yang **tidak** dimodelkan (dan dilaporkan di statistik): `restriction:conditional`
(berbasis waktu), `except=` untuk moda selain mobil, dan via-way yang tidak bisa
dirantai. Via-way dilarang secara konservatif (blokir masuk via-way dari arah
yang dibatasi) — kalau ini membuat tujuan tak terjangkau, router otomatis ulang
tanpa restriction dan menandai `"fallback": true` di respons.

Biaya format: 8 B per pasangan. Jawa: ~4.1 k pasangan = **32 KB** (0,003% dari
graf). Bandingkan tabel CSR penuh: 8 B x 46,7 jt edge = 374 MB.

## Format file

Lihat doc comment `internal/graph/format.go`. Header 256 B + 14 section
(Lat, Lon, CSR Off/To/Len/Time, geometri interior, grid, lalu section
`Ban`/`Only` berisi pasangan edge terlarang/diizinkan). Versi format 2;
file versi 1 tetap terbaca dengan restriction dimatikan.

## Roadmap (urutan dampak)

1. **Snap ke edge** (proyeksi ke ruas), bukan ke node terdekat.
3. **Ferry** sudah ikut, tapi belum ada penalti waktu tunggu.
4. **Build nasional**: 1.66 GB pbf, ~5 GB RAM dengan `GOMEMLIMIT` (streaming
   extractor bisa menurunkannya ke <1 GB). Runtime tetap ringan karena mmap.
5. **Hierarchy/CH** kalau butuh rute >60 km atau <20 ms.
6. **Validation harness**: 300-500 pasang koordinat vs Google Routes API,
   target jarak ±5-8% untuk 95% sampel.
7. Profil **motor** (`motorcycle=yes`, gang, `highway=service`) — sekarang mobil.

## Catatan data

## Lisensi

Kode: MIT (lihat `LICENSE`). Data: © OpenStreetMap contributors, ODbL —
wajib tampilkan atribusi "© OpenStreetMap contributors" di app, dan turunan
database (file `.graph`) tetap ODbL.
Update: ekstrak ulang bulanan lalu swap file + restart (74 s untuk Java).
