# HTTP API

Server: `nusa-serve` (satu profil) atau `nusa-gateway` (mobil + motor di satu
base URL). Tidak ada autentikasi. Base URL contoh: `http://127.0.0.1:8080`.

## Ringkasan endpoint

| Method | Path | Guna |
|---|---|---|
| `GET` | `/route` | rute dari query string |
| `POST` | `/route` | rute dari JSON body |
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
| `legs` | rute lintas region dijahit dengan konektor ferry |
| `trimmed: {from,to}` | spur awal/akhir dibuang |
| `fallback: true` | restriction dimatikan agar rute ditemukan |
| `relaxed: true`, `epsilon`, `turn_penalty` | budget habis, heuristic diperlonggar |

`ferry_spans` adalah rentang indeks inklusif ke array geometri; dipakai untuk
menggambar garis putus-putus.

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

Field yang didukung: `from`, `to`, `format`, `simplify`, `restrictions`, `pin`.
Field lain diabaikan. Body yang bukan JSON valid → `400`.

## Status dan error

| Status | `reason` | Arti |
|---|---|---|
| `200` | — | rute ditemukan |
| `304` | — | `If-None-Match` cocok; tidak ada body |
| `400` | — | parameter/JSON/koordinat tidak valid |
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
