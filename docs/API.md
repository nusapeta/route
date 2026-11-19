# HTTP API

Server: `nusa-serve` (satu profil) atau `nusa-gateway` (mobil + motor di satu
base URL). Tidak ada autentikasi. Base URL contoh: `http://127.0.0.1:8080`.

## Ringkasan endpoint

| Method | Path | Guna |
|---|---|---|
| `GET` | `/route` | rute dari query string |
| `GET` | `/matrix` | matriks satu-sumber banyak-tujuan |
| `GET` | `/isochrone` | area terjangkau dalam batas waktu (GeoJSON) |
| `POST` | `/route` | rute dari JSON body |
| `GET` | `/nearest` | snap koordinat ke ruas jalan tanpa pencarian rute |
| `GET` | `/healthz` | kesehatan + metadata graph |
| `GET` | `/version` | commit, build time, Go/OS/arch |
| `GET` | `/metrics` | Prometheus text format |
| `GET` | `/place/*` | geocoder bundle (hanya gateway dengan `-place`) |
| `OPTIONS` | `*` | preflight CORS |

## `GET /place/*`

Gateway dengan `-place` meneruskan prefix itu ke upstream geocoder terpisah
dan membuang prefix-nya, sehingga path yang didokumentasikan upstream tetap
berlaku apa adanya:

```http
GET /place/search?q=monas&limit=2
GET /place/reverse?lat=-6.175392&lon=106.827153
GET /place/lookup?osm_ids=N1308678148
GET /place/healthz
```

| Path | Upstream melihat |
|---|---|
| `/place/search` | `/search` |
| `/place/reverse` | `/reverse` |
| `/place/lookup` | `/lookup` |
| `/place/healthz` | `/healthz` |

Aturan bundle:

- `/place` saja menjawab `404`: upstream tidak punya halaman index.
- `/place/metrics` menjawab `404` saat `-block-metrics` aktif, sama seperti
  `/metrics`; endpoint itu memuat ukuran dan umur index.
- Rate limit terpisah per IP (default 240/menit, burst 40) menjawab `429`
  dengan `Retry-After`. Kuota routing tidak terpakai oleh lalu lintas geocoder.
- `X-Forwarded-For` dan `X-Real-IP` ditimpa dari `CF-Connecting-IP`, jadi
  klien tidak bisa memilih bucket sendiri.
- `/healthz` hanya melaporkan profil routing. Upstream geocoder yang mati
  membuat `/place/*` menjawab `502` tanpa membuat gateway dianggap gagal.

## `GET /route`

```http
GET /route?from=-6.1754,106.8272&to=-6.5950,106.8166&format=polyline
```

| Parameter | Wajib | Default | Nilai |
|---|---|---|---|
| `from` | ya | — | `lat,lng` atau `{"lat":..,"lng":..}` |
| `to` | ya | — | sama seperti `from` |
| `format` | tidak | `json` | `json`, `points`, `geojson`, `polyline` |
| `simplify` | tidak | `0` | meter; Douglas-Peucker, `0` = mati |
| `restrictions` | tidak | `1` | `0` mematikan enforcement (debug) |
| `pin` | tidak | `0` | `1` menambahkan titik mentah di awal/akhir |
| `profile` | tidak | `mobil` | hanya gateway: `mobil` atau `motor` |
| `avoid` | tidak | — | `toll`, `ferry`, `unpaved`, `motorway`; pisahkan dengan `,` atau `|` |
| `alt` | tidak | `0` | `1`…`3` rute alternatif; maksimal 3 putaran |
| `via` | tidak | — | `lat,lng|lat,lng|…`, maksimal 5 titik |

Koordinat harus di dalam Indonesia (lat −12…8, lon 93…142). Latitude dulu,
bukan longitude. Response selalu mengembalikan `from`/`to` hasil parse supaya
kesalahan urutan langsung terlihat.

### Format respons

Semua format membawa field dasar:

```json
{
  "from": [-6.1754, 106.8272],
  "to": [-6.5950, 106.8166],
  "distance_m": 56451,
  "duration_s": 2538.8,
  "region": "java",
  "snap_from_m": 4,
  "snap_to_m": 24,
  "snap_from": [-6.175401, 106.827203],
  "snap_to": [-6.594998, 106.816604],
  "bbox": [-6.5950, 106.8166, -6.1754, 106.8272],
  "points_n": 412
}
```

| `format` | Field tambahan |
|---|---|
| `json` / `points` | `points`: array `[lat, lng]` |
| `geojson` | `geojson`: `FeatureCollection` dengan `LineString` `[lng, lat]` |
| `polyline` | `polyline` (Google encoded, presisi 5) + `polyline_b64` |

`polyline` memakai charset yang mengandung `\`. Di JSON karakter itu menjadi
`\\`, sehingga copy-paste dari JSON viewer bisa merusak decoder naif. Untuk
aplikasi, pakai `polyline_b64` lalu base64-decode.

Field kondisional:

| Field | Muncul saat |
|---|---|
| `ferry_m`, `has_ferry`, `ferry_spans` | rute memakai ferry |
| `ferry_time_source` | rute lintas region: `osm` (durasi terukur) atau `schedule` (tabel statis); juga pada leg ferry |
| `legs` | rute via atau lintas region dijahit dengan konektor ferry |
| `alternatives` | `alt>0` dan ada rute alternatif |
| `trimmed: {from,to}` | spur awal/akhir dibuang |
| `fallback: true` | restriction dimatikan agar rute ditemukan |
| `relaxed: true`, `epsilon`, `turn_penalty` | budget habis, heuristic diperlonggar |

`ferry_time_source` mengambil `osm` bila connector memakai durasi OSM terukur,
`schedule` bila durasinya berasal dari tabel statis.

`ferry_spans` adalah rentang indeks inklusif ke array geometri; dipakai untuk
menggambar garis putus-putus.

### Restriction

Graph v5 menyimpan dua aturan belok yang ditegakkan saat routing: pasangan
`no_*` (`TurnOff`) dan penerus `only_*` (`TurnTo`), untuk profil mobil dan
motor. `restriction:conditional` **tidak didukung**. Relasi dengan tag itu
dihitung sebagai `conditional` di statistik build lalu dibuang; ia tidak masuk
graph dan tidak menambah `fallback`.

Alasannya eksplisit: graph statis tidak memiliki waktu berangkat atau model zona
waktu Indonesia, tidak ada fixture yang memvalidasi kapan suatu syarat aktif,
dan jendela waktu v6 mensyaratkan pembaruan anggaran artefak kolom 07 ENG
terlebih dahulu. Menegakkan semua syarat secara konservatif akan menutup jalan
di luar jam berlakunya, sehingga tidak dipilih. Klien yang memerlukan pembatasan
berbasis waktu harus menanganinya di luar Nusapeta Route.

`fallback: true` pada respons tetap berarti enforcement dimatikan karena rute
terkunci oleh aturan yang tersimpan. Relasi conditional yang dibuang tidak
memicunya.

### `avoid`

`avoid` menyaring ruas saat ekspansi A*, bukan setelah rute selesai. Jadi
`avoid=toll,motorway` mengubah simpul yang dikunjungi, snapping endpoint, dan
jumlah ekspansi (`/metrics` dan `X-Route-Expansions`). Kelas diperkira dari
kelas kecepatan artefak: tol/motorway = 80 km/h ke atas, `unpaved` = 10 km/h ke
bawah, `ferry` memakai indeks ferry. Tanpa tag OSM per-ruas di artefak ini,
pemetaan bersifat aproksimasi.

```http
GET /route?from=-6.1754,106.8272&to=-6.5950,106.8166&avoid=toll
```

Nilai selain keempat nilai di atas → `400`.

### `alt`

`alt=1`…`3` menjalankan hingga tiga putaran tambahan. Setiap putaran
melarang tepi dari rute yang sudah diterima, lalu mencari lagi; satu rute
diterima hanya bila paling sedikit 20% panjang tepinya berbeda dari rute
utama. Latensi dibatasi oleh plafon ekspansi yang sama dengan rute biasa.
Field `alternatives` berisi `distance_m`, `duration_s`, `points`, `points_n`
untuk tiap rute tambahan.

### `via`

`via` memecah perjalanan menjadi leg berturut-turut. Jumlah `legs` =
jumlah titik (termasuk `from` dan `to`) dikurangi satu; `distance_m` total
= jumlah `distance_m` leg (±1 m). Tiap leg membawa `duration_s`,
`start_index`, `end_index`, `points_n` yang menunjuk ke array geometri.
Rute lintas-region belum didukung untuk `via` → `422 cross_region`.

## `GET /nearest`

```http
GET /nearest?lat=-6.1754&lon=106.8272&k=2&max=3000
```

| Parameter | Wajib | Default | Nilai |
|---|---|---|---|
| `lat`, `lon` | ya | — | derajat desimal |
| `k` | tidak | `1` | `1`…`32` jumlah kandidat |
| `max` | tidak | `-snap` | radius meter, maksimal 100000 |

Mengembalikan `region` dan `candidates` berisi `distance_m`, `lat`, `lon`,
`node`, `edge`, `t`. Kandidat pertama identik dengan `SnapEdge` tunggal
pada parameter yang sama. Endpoint ini tidak menjalankan A*: ekspansi tetap
nol, cocok untuk preview pin saat pengguna masih menggeser peta.

## `POST /route`

Body maksimum 8 KiB. `from`/`to` boleh string atau objek:

```bash
curl -s -X POST http://127.0.0.1:8080/route \
  -H 'Content-Type: application/json' \
  -d '{
    "from": {"lat": -6.1754, "lng": 106.8272},
    "to":   {"lat": -6.5950, "lng": 106.8166},
    "format": "geojson",
    "simplify": 5
  }'
```

Field yang didukung: `from`, `to`, `format`, `simplify`, `restrictions`, `pin`,
`avoid`, `alt`, `via`. `avoid` berupa string (`"toll,ferry"`), `alt` bilangan
`0`…`3`, `via` string `"lat,lng|lat,lng"`. Field lain diabaikan. Body yang
bukan JSON valid → `400`.

## `GET /matrix`

Matriks satu-sumber banyak-tujuan untuk pembeli logistik. Satu Dijkstra per
sumber; semua tujuan dari sumber itu dibaca dari pencarian yang sama.

```http
GET /matrix?sources=-6.2,106.8&targets=-6.2,106.8012|-6.1988,106.8024&max_minutes=30
```

| Parameter | Wajib | Default | Nilai |
|---|---|---|---|
| `sources` | ya | — | 1–5 titik `lat,lng`, dipisah `|` (boleh param berulang) |
| `targets` | ya | — | 1–25 titik `lat,lng`, dipisah `|` |
| `max_minutes` | tidak | `60` | 1–180 menit |

`sources × targets` maksimal 25. Pasangan dengan jarak garis lurus >100 km
menjadi `too_far` tanpa pencarian. Titik yang gagal di-snap menjadi
`snap_failed`; kalau semua titik gagal, responsnya `422 snap`.

```json
{
  "sources": [[-6.2, 106.8]],
  "targets": [[-6.2, 106.8012], [-6.1988, 106.8024]],
  "matrix": [
    [
      {"status": "ok", "distance_m": 134, "duration_s": 60.2},
      {"status": "no_route"}
    ]
  ],
  "meta": {"max_minutes": 60, "expansions": 120, "elapsed_ms": 3}
}
```

Status sel: `ok`, `too_far`, `snap_failed`, `no_route`. Biaya memakai waktu
tanpa biaya belokan; `distance_m` dan `duration_s` sudah menyertakan potongan
snap. Endpoint di region berbeda tanpa konektor ferry menjadi `no_route`.

Satu sel juga bisa `no_route` padahal `/route` untuk pasangan yang sama
menjawab 200. Pencarian matrix memakai satu snap per titik dan satu Dijkstra
tanpa percobaan ulang, sedangkan `/route` mengulang snap ke kandidat lain saat
edge terdekat masuk kantong mati. Contoh terukur pada graph nasional 19 November
2026: `-6.1754,106.8272 -> -6.2,106.8166` menjadi `no_route` di `/matrix`,
tetapi `/route` menjawab 4.808 m.

## `GET /isochrone`

Area yang bisa dicapai dari satu titik dalam batas waktu tertentu. Respons
adalah GeoJSON `FeatureCollection`; tiap `Feature` satu ring poligon tertutup
dengan koordinat `[lng, lat]`.

```http
GET /isochrone?lat=-6.1754&lon=106.8272&minutes=15&meters=5000&grid=250&simplify=25
```

| Parameter | Wajib | Default | Nilai |
|---|---|---|---|
| `lat`, `lon` | ya | — | derajat; `lng` diterima sebagai alias `lon` |
| `minutes` | tidak | `15` | `1`…`60` |
| `meters` | tidak | `100000` | `1`…`100000` |
| `grid` | tidak | `250` | `50`…`10000` meter; otomatis naik bila melebihi 40000 sel |
| `simplify` | tidak | `25` | meter; Douglas-Peucker, `0` = mati |
| `profile` | tidak | `mobil` | hanya gateway: `mobil` atau `motor` |

Ring dibuat dari raster sel yang tercapai oleh Dijkstra dengan batas `minutes`;
ukuran sel aktif setelah penyesuaian otomatis dilaporkan di `meta.grid_m`. Ring
minimal 8 vertex dan luas minimal 2000 m²; sisanya dibuang.

```json
{
  "type": "FeatureCollection",
  "features": [
    {"type": "Feature", "geometry": {"type": "Polygon", "coordinates": [[[106.827, -6.175], [106.828, -6.176], [106.827, -6.175]]]}}
  ],
  "meta": {"grid_m": 500, "nodes_visited": 3480, "area_m2": 16000000, "minutes": 15}
}
```

## Status dan error

| Status | `reason` | Arti |
|---|---|---|
| `200` | — | rute ditemukan |
| `304` | — | `If-None-Match` cocok; tidak ada body |
| `400` | `matrix_too_large` | lebih dari 5 sumber atau `sources × targets` > 25 |
| `400` | `too_many_targets` | lebih dari 25 target |
| `400` | `cutoff_too_large` | `max_minutes` > 180 |
| `422` | `snap` | tidak ada jalan dalam radius `-snap` (default 3 km) |
| `422` | `too_far` | jarak garis lurus atau jarak tempuh > `-max-route-km` |
| `422` | `budget` | budget lunak habis; rute terlalu panjang |
| `422` | `no_route` | graph tidak terhubung |
| `422` | `cross_region` | endpoint di region berbeda tanpa konektor ferry |
| `429` | — | rate limit per IP; ada `Retry-After` |
| `500` | — | error internal; detail hanya di log |
| `504` | `deadline` | deadline keras `-timeout` habis |

Bentuk error:

```json
{"error": "no road within snap distance (region not loaded?)", "reason": "snap"}
```

`reason` adalah kosakata stabil untuk klien. Teks `error` bisa berubah. Semua
error memakai `Cache-Control: no-store` dan tidak membawa ETag.

## Cache dan ETag

- `-maxage 3600` (default) mengaktifkan `ETag` + `Cache-Control: public,
  max-age=3600` pada `200`.
- `304` juga membawa ETag; body kosong.
- Validator dihitung dari fingerprint graph + parameter rute, bukan dari hasil
  waktu. Graph yang berubah menghasilkan ETag berbeda.
- Pengecekan `If-None-Match` dilakukan **sebelum** rate limit, sehingga
  revalidasi cache tidak menghabiskan token.
- `429` tidak pernah membawa ETag; error tidak boleh di-cache.
- `-maxage 0` mematikan ETag dan `Cache-Control` sukses.

## Rate limit dan deadline

Default: 15 request/menit per IP, burst 5. Token bucket di memori proses,
bukan shared antar-container. Di belakang proxy, jalankan dengan
`-trust-proxy` dan pastikan proxy menimpa `X-Forwarded-For`/`CF-Connecting-IP`.

- `-timeout 10s`: deadline keras. Search yang melewati → `504 deadline`.
- `-budget`: budget lunak, default 60% timeout. Search yang melewati → `422
  budget`, deterministik dan tidak bergantung beban.
- `-max-expansions 4000000`: plafon ekspansi per query.
- `-max-route-km 100`: tolak rute >100 km; `0` mematikan.
- `-conc 4`: jumlah query paralel; sisanya antre.
- `-workspace-mb 256`: total tabel A*, dibagi rata ke `-conc`.

Rate limit dihitung per proses. Dua container profil mobil/motor punya bucket
masing-masing; gateway tidak menambah rate limit sendiri.

## Header respons

| Header | Isi |
|---|---|
| `X-Route-Ms` | wall time routing, milidetik |
| `X-Matrix-Ms` | wall time pencarian matriks, milidetik |
| `X-Route-Region` | nama file graph yang melayani |
| `X-Route-Expansions` | jumlah edge yang diekspansi |
| `X-Route-Trim` | `none`, `from`, `to`, atau `both` |
| `X-Route-Profile` | `mobil`/`motor`, hanya gateway |
| `ETag`, `Cache-Control` | hanya sukses/304 |
| `Retry-After` | hanya 429 |
| `Access-Control-Expose-Headers` | daftar header di atas untuk browser |

## `/healthz`

Response server tunggal:

```json
{
  "ok": true,
  "version": "dev",
  "commit": "e1c4b0e",
  "regions": [
    {
      "name": "indonesia",
      "nodes": 54891393,
      "edges": 112340071,
      "min_lat": -11.02, "min_lon": 94.97,
      "max_lat": 6.08, "max_lon": 141.02,
      "bans": 3694,
      "only_rules": 154,
      "restrictions_skipped": 593,
      "format": 5,
      "pbf_sha256": "752489d3...",
      "built_at": "2026-11-12T11:12:59Z",
      "source": "indonesia-latest.osm.pbf",
      "extractor": "nusa-extract",
      "profile": "car+motor",
      "profile_sha256": "..."
    }
  ]
}
```

`ok` bernilai `true` bila minimal satu region terbuka. Field provenance bisa
kosong untuk graph lama. Gateway mengubah bentuknya:

```json
{
  "ok": true,
  "profiles": {
    "mobil": {"ok": true, "status": 200, "upstream": { ... }},
    "motor": {"ok": true, "status": 200, "upstream": { ... }}
  }
}
```

Gateway menganggap sehat hanya jika kedua upstream sehat. Jangan memakai
`/healthz` sebagai bukti bahwa container tertentu sedang running; itu tugas
`docker ps`/systemd.

## `/version`

```json
{"commit":"e1c4b0e","built_at":"2026-11-12T...","go_version":"go1.27.3","goos":"linux","goarch":"amd64"}
```

`commit`/`built_at` diisi lewat `-ldflags` saat `make build`/`make static`.
Tanpa itu nilainya `dev`/`unknown`.

## `/metrics`

Prometheus text format, tanpa dependency. Metrik utama:

| Metrik | Tipe | Guna |
|---|---|---|
| `route_requests_total{code}` | counter | status 200/304/400/422/429/500/504 |
| `route_request_duration_seconds` | histogram | wall time `/route` |
| `route_inflight` | gauge | request sedang berjalan |
| `route_expansions_total` | counter | total ekspansi A* |
| `route_fallback_total` | counter | rute tanpa restriction |
| `route_relaxed_total` | counter | rute dengan ε diperlonggar |
| `route_rate_limited_total` | counter | ditolak limiter |
| `route_canceled_total` | counter | deadline keras |
| `route_errors_total{reason}` | counter | `snap`, `too_far`, `budget`, `no_route`, `cross_region` |
| `route_snap_retry_total` | counter | snap alternatif setelah no-route |
| `route_cross_region_stitched_total` | counter | rute ferry lintas region |
| `nusa_route_workspace_slots` | gauge | total slot tabel A* |
| `nusa_route_workspace_bytes` | gauge | estimasi 12 B/slot |
| `nusa_route_workspace_grows_total` | counter | rehash tumbuh |
| `nusa_route_workspace_shrinks_total` | counter | rehash menyusut |
| `nusa_route_build_info{commit,go_version}` | gauge | identitas build |

Contoh query:

```bash
curl -s 127.0.0.1:18080/metrics | grep -E '^route_(requests|errors|expansions)|^nusa_route_workspace_'
```

## Integrasi Flutter

Gunakan `polylines` + `latlong2`. Decode `polyline_b64` agar tidak tersandung
escape JSON:

```dart
import 'dart:convert';

List<List<double>> decodePolyline(String encoded) {
  var index = 0, lat = 0, lng = 0;
  final out = <List<double>>[];
  while (index < encoded.length) {
    var shift = 0, result = 0, b = 0;
    do {
      b = encoded.codeUnitAt(index++) - 63;
      result |= (b & 0x1f) << shift;
      shift += 5;
    } while (b >= 0x20);
    lat += (result & 1) != 0 ? ~(result >> 1) : (result >> 1);

    shift = 0;
    result = 0;
    do {
      b = encoded.codeUnitAt(index++) - 63;
      result |= (b & 0x1f) << shift;
      shift += 5;
    } while (b >= 0x20);
    lng += (result & 1) != 0 ? ~(result >> 1) : (result >> 1);

    out.add([lat / 1e5, lng / 1e5]);
  }
  return out;
}

final json = jsonDecode(response.body) as Map<String, dynamic>;
final raw = utf8.decode(base64Decode(json['polyline_b64'] as String));
final points = decodePolyline(raw)
    .map((p) => LatLng(p[0], p[1]))
    .toList();
```

Untuk cache, simpan `ETag` dan kirim `If-None-Match`; `304` berarti polyline
lama masih valid. Jangan cache `422`/`429`/`504`.

## Tooling

- Koleksi Bruno: `contrib/http/bruno/nusapeta-route/`.
- Postman: `contrib/http/nusapeta-route.postman_collection.json`.
- Smoke test: `scripts/smoke.sh http://127.0.0.1:8080`.

Bruno/Postman memakai contoh koordinat dan assertion status; perbarui keduanya
saat kontrak berubah. File Bruno pengguna yang sedang dimodifikasi jangan
ditimpa otomatis.
