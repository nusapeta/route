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
| Turn restriction | belum | ya |
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
go build -o bin/rid-extract ./cmd/rid-extract
go build -o bin/rid-server  ./cmd/rid-server
go build -o bin/rid-route   ./cmd/rid-route

# build graph (Java: 22.7 M node, 74 s, ~2 GB RAM)
bin/rid-extract -pbf java-latest.osm.pbf -out java.graph

# cek rute
bin/rid-route -graph java.graph -from -6.1754,106.8272 -to -6.5950,106.8166

# server
bin/rid-server -graph java.graph -addr :8080 -conc 4
curl 'localhost:8080/route?from=-6.1754,106.8272&to=-6.5950,106.8166&format=polyline'
```

Response:
```json
{"distance_m":56451,"duration_s":2538.8,"polyline":"...","snap_from_m":311,"snap_to_m":61}
```
`format=json` (default) mengembalikan `points: [[lat,lon],...]`.
Polyline: precision **5** (~1.1 m), decode di Dart standar.

Query params: `simplify=<meter>` (Douglas-Peucker), `format=polyline|json`.

## Multi-region (per pulau)

Graph bisa dipecah per pulau lalu disajikan satu server; region dipilih dari
snap terdekat (bbox antar-extract saling tumpang tindih):

```bash
bin/rid-server -graph java.graph,sumatra.graph -addr :8080
bin/rid-server -graph data/ -addr :8080          # semua *.graph di direktori
```

Terukur:
| region | node | edge | disk | build | peak RAM build |
|---|---|---|---|---|---|
| java | 22.7 M | 46.7 M | 972 MB | 74 s | 3.8 GB |
| sumatra | 11.9 M | 24.3 M | 533 MB | 18 s | 1.2 GB |
| nasional (est.) | ~44 M | ~90 M | ~2.0-2.2 GB | ~3 mnt | ~7-8 GB |

Rute lintas-region (mis. Merak -> Bakauheni) belum didukung. Kalau butuh,
gabungkan jadi satu graph dari pbf nasional (butuh mesin 16 GB saat build).

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

## Format file

Lihat doc comment `internal/graph/format.go`. Header 256 B + 14 section
(Lat, Lon, CSR Off/To/Len/Time, geometri interior, grid, turn restriction).

## Roadmap (urutan dampak)

1. **Turn restriction** (`type=restriction`) → edge-based graph. Ini yang
   membedakan "akurat" dan "kadang ngawur" di Jakarta/Bandung.
2. **Snap ke edge** (proyeksi ke ruas), bukan ke node terdekat.
3. **Ferry** sudah ikut, tapi belum ada penalti waktu tunggu.
4. **Build nasional**: 1.66 GB pbf, butuh ~10-16 GB RAM saat ekstrak.
   Sisanya runtime ringan karena mmap.
5. **Hierarchy/CH** kalau butuh rute >60 km atau <20 ms.
6. **Validation harness**: 300-500 pasang koordinat vs Google Routes API,
   target jarak ±5-8% untuk 95% sampel.
7. Profil **motor** (`motorcycle=yes`, gang, `highway=service`) — sekarang mobil.

## Catatan data

Data © OpenStreetMap contributors (ODbL). Wajib atribusi di app.
Update: ekstrak ulang bulanan lalu swap file + restart (74 s untuk Java).
