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
4. **Build nasional**: 1.66 GB pbf, butuh ~10-16 GB RAM saat ekstrak.
   Sisanya runtime ringan karena mmap.
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
