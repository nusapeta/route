# Operasi

Panduan ini untuk membangun, menjalankan, dan merawat Nusapeta Route. Langkah deploy
host yang spesifik (Compose, systemd, Cloudflare Tunnel) ada di
[`../deploy/README.md`](../deploy/README.md); dokumen ini menjelaskan
kontraknya.

## Prasyarat

- Go 1.27+ untuk build dari source.
- Linux amd64/arm64. `mmap` graph memakai `syscall.Mmap`.
- RAM: 2 GB untuk build region, ±5 GB untuk graph nasional.
- Disk: PBF Indonesia 1,7 GB + graph 1,6 GB + ruang untuk file sementara.
- SSD. Halaman graph dibaca saat disentuh; HDD membuat query pertama lambat.

## Build

```bash
make build          # binary dinamis ke bin/
make static         # binary statis ke bin/static/ untuk Alpine/distroless
make test           # go vet + seluruh test
```

`make build` menulis commit dan waktu build lewat `-ldflags`. Tanpa itu
`/version` melaporkan `dev`/`unknown`. `make static` memakai `CGO_ENABLED=0`,
`-trimpath`, dan `SOURCE_DATE_EPOCH` bila tersedia.

Ukuran binary saat ini: `bin/nusa-serve` ±6,7 MB. Image `nusapeta-route:local` yang
dibangun dari Dockerfile multi-stage terukur 36,7 MB; varian prebuilt lebih
kecil karena hanya menyalin binary ke `alpine:3.22`.

> **Jebakan Alpine:** binary hasil `go build` biasa ter-link ke glibc. Di image
> musl, hasilnya `exec /usr/local/bin/nusa-serve: no such file or directory`.
> Selalu pakai `make static` untuk image.

## Data dan graph

Sumber: Geofabrik Indonesia.

```bash
scripts/fetch.sh java          # data/java-latest.osm.pbf
scripts/fetch.sh indonesia     # data/indonesia-latest.osm.pbf
```

Build region:

```bash
make graph REGION=java MEMLIM=2GiB
# setara:
GOMEMLIMIT=2GiB bin/nusa-extract \
  -pbf data/java-latest.osm.pbf -out data/java.graph
```

Build nasional:

```bash
GOMEMLIMIT=5GiB scripts/build-national.sh
```

Waktu terukur di Ryzen 5 5600G: unduh + ekstrak 11m40s, ekstraksi saja 1m12s.
Graph nasional v5: 54.891.393 node / 112.340.071 edge, 1.621.222.584 B.

Inspeksi:

```bash
bin/nusa-graphinfo -graph data/indonesia.graph
bin/nusa-graphinfo -graph data/indonesia.graph -at=-2.5916,140.6690
```

`-at` melakukan snap ke koordinat dan menampilkan edge/kelas; berguna untuk
memastikan graph tidak bolong di lokasi yang dilaporkan.

### Refresh bulanan

```bash
make refresh REGION=java
```

Target ini mengunduh ulang, membangun graph, menulis `tagstats` dan
`graphinfo`, menjalankan test, lalu A/B singkat. Untuk nasional:

```bash
GOMEMLIMIT=5GiB scripts/build-national.sh
scripts/smoke.sh http://127.0.0.1:8080
```

Jangan menimpa graph yang sedang dipakai server secara langsung. Hentikan
proses, ganti file, lalu start:

```bash
mv data/indonesia.graph data/indonesia.graph.prev
mv data/indonesia.graph.new data/indonesia.graph
sudo systemctl restart nusapeta-route
```

Server memetakan file read-only; mengganti isi file di tempat saat proses
berjalan bisa membuat halaman tidak konsisten. Simpan graph lama minimal satu
versi untuk rollback.

### Provenance

Setiap graph menyimpan SHA-256 PBF, waktu build, versi extractor, profil, dan
ringkasan restriction. Cek lewat `/healthz`. Jika `pbf_sha256` tidak cocok
dengan file PBF yang ada, graph berasal dari unduhan lain; jalankan ulang
ekstraksi.

## Menjalankan server

Satu profil:

```bash
bin/nusa-serve -graph data/indonesia.graph -addr 127.0.0.1:8080
```

Dua profil + gateway:

```bash
bin/nusa-serve -graph data/indonesia.graph -profile car   -addr 127.0.0.1:18080
bin/nusa-serve -graph data/indonesia.graph -profile motor -addr 127.0.0.1:18081
bin/nusa-gateway -addr 127.0.0.1:8080
```

Bundle geocoder (opsional, upstream milik project lain):

```bash
bin/nusa-gateway -addr 127.0.0.1:8080 \
  -place http://placeid:8090 -place-rate 240 -place-burst 40
```

Flag yang paling berpengaruh:

| Flag | Default | Efek |
|---|---|---|
| `-conc` | 4 | query paralel; memori dan throughput naik |
| `-workspace-mb` | 0 | total tabel A*; 0 = 384 MB/slot |
| `-max-route-km` | 100 | tolak rute lebih panjang; 0 = mati |
| `-timeout` | 10s | deadline keras |
| `-budget` | 0 | budget lunak; 0 = 60% timeout |
| `-max-expansions` | 4.000.000 | plafon ekspansi |
| `-eps` | 1.5 | bobot heuristik |
| `-turn-penalty` | 0.25 | biaya belok |
| `-snap` | 3000 | radius snap, meter |
| `-rate` / `-burst` | 15 / 5 | rate limit per IP |
| `-trust-proxy` | false | baca IP dari proxy |
| `-maxage` | 3600 | ETag/Cache-Control |

Untuk mesin kecil: `-conc 1` dan `GOMEMLIMIT=512MiB`. Untuk 2 core/4 thread,
`-conc 4` memberi throughput lebih tinggi karena query didominasi I/O halaman
dan GC, bukan CPU murni.

Flag `nusa-gateway`:

| Flag | Default | Efek |
|---|---|---|
| `-mobil` / `-motor` | `127.0.0.1:18080` / `:18081` | upstream per profil |
| `-default` | `mobil` | profil saat `?profile=` tidak ada |
| `-trust-proxy` | false | ambil IP klien dari `CF-Connecting-IP` |
| `-block-metrics` | false | `/metrics` dan `/place/metrics` menjawab `404` |
| `-place` | kosong | upstream geocoder di prefix `/place`; kosong = bundle mati |
| `-place-rate` / `-place-burst` | 240 / 40 | kuota per menit dan burst per IP untuk `/place` |

## Docker Compose

```bash
cd deploy
COMMIT=$(git rev-parse --short HEAD) BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  docker compose up -d --build
docker compose ps
curl -s localhost:8080/healthz | jq .
```

Dua pilihan image:

| Pilihan | Perintah | Butuh |
|---|---|---|
| multi-stage | `docker compose up -d --build` | tarik `golang:1.27-alpine` |
| prebuilt | `NUSAPETA_ROUTE_DOCKERFILE=deploy/Dockerfile.prebuilt docker compose up -d --build` | binary dari `make static` |

Image tidak memuat data. Graph di-bind-mount dari host ke `/data:ro`. Batas
container `mem_limit: 1536m` cukup untuk graph nasional; halaman `mmap` yang
bersih bisa direklaim cgroup.

Status `docker compose ps` harus `healthy` setelah startup. Kalau tidak, baca
`docker logs nusapeta-route` dan `docker logs nusapeta-gateway`; jangan menganggap
`/healthz` satu container mewakili seluruh deployment.

## systemd

Unit contoh ada di `deploy/nusapeta-route.service`. Intinya:

```ini
[Service]
User=nusapeta
ExecStart=/srv/nusapeta-route/bin/nusa-serve -graph /srv/nusapeta-route/data/indonesia.graph -addr 127.0.0.1:8080
Restart=on-failure
LimitNOFILE=65536
```

Salin graph dan binary, lalu `systemctl daemon-reload && systemctl enable
--now Nusapeta Route`. Jangan jalankan sebagai root. Batasi akses port ke loopback.

## Cloudflare Tunnel

Tunnel memberi TLS di edge tanpa membuka port. Detail langkah ada di
[`../deploy/README.md`](../deploy/README.md). Yang perlu dijaga:

- gateway memegang port loopback 8080; upstream 18080/18081 tidak publik;
- `-trust-proxy` hanya aktif di belakang proxy yang menimpa
  `X-Forwarded-For`/`CF-Connecting-IP`;
- `/metrics` diblokir di gateway publik (`-block-metrics`), begitu juga
  `/place/metrics`;
- `/place` diteruskan ke container `placeid` di jaringan tunnel yang sama;
  kalau container itu mati, `/place/*` menjawab `502` dan `/healthz` tetap
  `200`, jadi gateway tidak ikut dianggap gagal;
- jangan menaruh token tunnel di repo.

## Monitoring

```bash
scripts/smoke.sh http://127.0.0.1:8080
scripts/memcheck.sh http://127.0.0.1:18080
curl -s 127.0.0.1:18080/metrics | grep -E '^route_|^nusa_route_workspace_'
```

Yang perlu dipantau:

| Metrik | Arti | Tindakan |
|---|---|---|
| `route_requests_total{code="422"}` | snap/terlalu jauh/budget | lihat `route_errors_total{reason}` |
| `route_errors_total{reason="budget"}` | rute kehabisan budget | naikkan `-timeout`/`-max-route-km` atau terima batas |
| `route_rate_limited_total` | klien kena limit | naikkan `-rate`/`-burst` atau pakai proxy |
| `nusa_route_workspace_bytes` | tabel A* | bandingkan dengan `-workspace-mb` |
| `nusa_route_workspace_shrinks_total` | reaper bekerja | normal setelah idle |
| `route_request_duration_seconds` | latency | bandingkan p50/p95, bukan rata-rata |

RSS yang tinggi belum tentu bocor: `mmap` graph menghitung halaman resident.
Cek `RssAnon` untuk memori heap/workspace, dan `RssFile` untuk halaman graph.

## Kapasitas

Angka terukur (i3-6100T, 2C/4T) dengan satu region:

| Rute | p50 | Throughput |
|---|---:|---:|
| 7 km | 47 ms | ±75 RPS |
| 15 km | 136 ms | ±17 RPS |
| 41,7 km | 93–102 ms | 36–40 RPS |
| 148 km | 4,2 s | ±0,6 RPS |

Beban campuran ≤40 km, `-conc 4`: ±24 RPS. Beban 100 km di Ryzen: p50 39–47 ms,
p95 0,94–1,32 s; 481/600 pasangan sukses. Angka lengkap dan batas klaim di
[`BENCHMARKS.md`](BENCHMARKS.md).

Aturan praktis:

- `-conc 4` untuk 2 core fisik; `-conc 1` untuk RAM ketat.
- Satu query panjang menahan satu core selama detik. Jangan campur ribuan
  query 100 km dengan latensi interaktif tanpa antrean.
- Rate limit gateway 15/menit adalah batas produk, bukan batas mesin.
- Graph 1,6 GB wajib SSD; page cache OS membantu query berulang.

## Rollback

- Simpan `data/indonesia.graph.prev` dan image lama (`docker tag`).
- Format v5 tidak bisa dibaca image pre-m5; rollback image harus menyertakan
  graph yang cocok.
- `docker compose down` tidak menghapus graph karena berada di host.
- Jangan commit `data/`, `bin/`, atau PDF build ke git.

## Keamanan

- Tidak ada autentikasi dan tidak ada TLS di server. Gunakan reverse proxy.
- CORS `*`; jangan kirim cookie/credential ke API ini.
- Rate limit per IP aktif default. Di belakang Cloudflare, `-trust-proxy`
  wajib dan proxy harus menimpa header IP.
- Batasi body POST 8 KiB (sudah di handler).
- Jangan expose `/metrics`; metrik membocorkan beban dan ukuran graph.
- Satu query rute panjang memakai CPU/detik; anggap ini endpoint mahal.

## Atribusi dan lisensi data

Graph adalah turunan OpenStreetMap. Tampilkan "© OpenStreetMap contributors"
di aplikasi dan halaman publik. Data ODbL; kode MIT. Detail di
[`../README.md`](../README.md) dan `LICENSE`.
