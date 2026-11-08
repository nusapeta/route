# Deploy di homelab (Jawa + Sumatra)

Dua cara: Docker Compose (paling cepat) atau systemd (tanpa Docker).
Dua-duanya memakai graph yang sudah dibangun di host; image tidak pernah
memuat data 1,5 GB.

## 0. Bangun graph (sekali, di host)

Build jauh lebih berat dari run: puncak ~2,5 GB (Java) dan ~1,4 GB (Sumatra).
Kalau RAM pas-pasan, batasi Go: `GOMEMLIMIT=1800MiB make java`.

```bash
make build
./bin/rid-extract -pbf data/java-latest.osm.pbf    -out data/java.graph
./bin/rid-extract -pbf data/sumatra-latest.osm.pbf -out data/sumatra.graph
ls -lh data/*.graph          # java ~972 MB, sumatra ~509 MB
```

## 1a. Docker Compose

Dua pilihan image:

| pilihan | cara | butuh |
|---|---|---|
| multi-stage | `docker compose up -d --build` | tarik image `golang:1.27-alpine` (~800 MB) |
| prebuilt (hemat) | `ROUTEID_DOCKERFILE=deploy/Dockerfile.prebuilt docker compose up -d --build` | hanya `alpine:3.22` (~4 MB) |

Untuk pilihan prebuilt, kirim binary statisnya dulu (jangan binary dinamis,
lihat catatan di bawah):

```bash
make static
rsync -a bin/static/ host:~/Projects/routeid/bin/
```

> **Jebakan yang sudah memakan waktu:** binary hasil `go build` biasa ter-link
> dinamis ke glibc. Di image alpine (musl) hasilnya error
> `exec /usr/local/bin/rid-server: no such file or directory`, pesan yang
> menyesatkan, filenya ada. Selalu pakai `make static`
> (`CGO_ENABLED=0`) untuk image, dan jangan rsync folder `bin/` mentah-mentah.



```bash
sudo usermod -aG docker "$USER"   # sekali, lalu login ulang
cd deploy
COMMIT=$(git rev-parse --short HEAD) BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  docker compose up -d --build
docker compose ps                 # STATUS harus (healthy)
curl -s localhost:8080/healthz    # version harus sama dengan COMMIT di atas
```

Tanpa `COMMIT`/`BUILT`, `/version` melaporkan `dev`/`unknown` dan tidak ada cara
mencocokkan image dengan commit. Di host yang bukan repo git, kirim nilainya
dari mesin pengembang, misalnya `COMMIT=5517c97 BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ)`.

Batas memori 1,5 GB (`mem_limit`: RSS memuat halaman `mmap` graf), graceful
stop 25 s, log diputar 3x10 MB.

## 1b. systemd (tanpa Docker)

```bash
sudo useradd --system --home /srv/routeid --shell /usr/sbin/nologin routeid
sudo mkdir -p /srv/routeid/bin && sudo cp bin/rid-server /srv/routeid/bin/
sudo cp -r data /srv/routeid/            # java.graph + sumatra.graph
sudo chown -R routeid:routeid /srv/routeid
sudo install -m 0644 deploy/routeid.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now routeid
systemctl status routeid --no-pager
```

Unit sudah membatasi `MemoryMax=1200M` dan mengeraskan filesystem
(`ProtectSystem=strict`, data read-only).

## 2. Verifikasi

```bash
scripts/smoke.sh http://127.0.0.1:8080
./bin/rid-graphinfo -graph data/sumatra.graph -at 3.5952,98.6722   # snap Medan
```

Contoh cepat:

```bash
curl -s "127.0.0.1:8080/route?from=-6.2088,106.8456&to=-6.9175,107.6191&format=geojson" \
  | python3 -c "import sys,json;d=json.load(sys.stdin);print(d['region'],d['distance_m'],'m')"
```

## 3. Resource saat run (terukur, mesin 2C/4T 7 GB)

| keadaan | RSS |
|---|---|
| idle | 15 MB |
| 20x rute 15 km | 89 MB |
| 1x rute 148 km | 401 MB |
| 1x rute 578 km | 613 MB |
| puncak, 600 pasang campuran (build `8e2dc11`) | ~1,1 GB (`anon` 415 MB) |
| beban kontrak 40 km, `-conc 4`, `GOMEMLIMIT=1GiB` | 958 MB (`anon` 311 MB) |

CPU 1 core penuh selama satu query: 128 ms (15 km), 3,6 s (148 km),
1,7 s (578 km). Disk 1,48 GB, wajib SSD (halaman mmap dibaca saat disentuh).
Startup 10 ms.

Untuk mesin kecil: `-conc 1` (memori ~4x lebih hemat, query diserialkan) dan
`GOMEMLIMIT=512MiB`.

Dua flag yang dipakai container sejak build `8e2dc11`:

- `-workspace-mb 256`: anggaran total tabel A*, dibagi rata ke `-conc` slot
  (12 B/slot, jadi 64 MB/slot pada `-conc 4`). Tabel mulai 0,75 MB, tumbuh saat
  rute panjang, dan menyusut balik bila kapasitas > 8x pemakaian. Bila anggaran
  habis, klien menerima `422 search space exhausted (route too long)`, bukan
  OOM. `0` = tanpa anggaran eksplisit (plafon lama 384 MB/slot).
- `-max-route-km 40`: kontrak aplikasi, rute di atas 40 km (garis lurus lebih
  dulu, lalu jarak tempuh) ditolak `422`, jadi segmen <= 40 km tetap di p50
  24-73 ms dan p95 246-705 ms (`paper/RESULTS.md` §16).

Ukur sendiri sesudah beban:

```bash
scripts/memcheck.sh http://127.0.0.1:8080            # gauge workspace + smaps_rollup
curl -s http://127.0.0.1:8080/metrics | grep ^routeid_workspace_
```

## 3b. Kapasitas (terukur di i3-6100T, 2C/4T)

| rute | server p50 | RPS puncak |
|---|---|---|
| 7 km | 47 ms | ~75 |
| 15 km | 136 ms | ~17 |
| 148 km | 4,2 s | ~0,6 |

Konfigurasi di `docker-compose.yml` sengaja `-conc 4` (2 core fisik): dengan
`-conc 1` throughput rute 15 km cuma ~7 RPS. Klien lebih dari 4 bersamaan
hanya menambah antrean. Ukur sendiri:

```bash
scripts/loadtest.py http://127.0.0.1:8080 -from -7.8435,110.3929 -to -7.7393,110.3760 -c 4 -n 32
```

Untuk pemakaian bersama, batasi di reverse proxy (Caddy):

```
routeid.lan {
    rate_limit {
        zone routeid { key {remote_host} events 30 window 1m }
    }
    reverse_proxy 127.0.0.1:8080 {
        transport http { response_header_timeout 30s }
    }
}
```

## 4. Keamanan

Server ini **tanpa autentikasi**, `Access-Control-Allow-Origin: *`, dan rate
limit per IP aktif secara default (`-rate 15 -burst 5`, lihat M4 di bawah).
Untuk homelab:

- bind ke LAN saja: `-addr 192.168.x.x:8080`, jangan `0.0.0.0` bila tak perlu.
- taruh di belakang reverse proxy (Caddy/nginx) kalau harus HTTPS; proxy juga
  sebaiknya membatasi ukuran/rate.
- jangan expose ke internet tanpa auth + rate limit: satu query rute panjang
  memakan 1 core selama beberapa detik.

## 5. Update data OSM

```bash
curl -O https://download.geofabrik.de/asia/indonesia/java-latest.osm.pbf
make build && ./bin/rid-extract -pbf java-latest.osm.pbf -out data/java.graph
docker compose restart      # atau: sudo systemctl restart routeid
```

Untuk update tanpa downtime: bangun ke nama lain (`java-new.graph`), lalu
jalankan dua server di port berbeda dan alihkan proxy.

## M4: cache, batas waktu, batas laju, metrics

Compose sekarang meneruskan `-timeout 10s -rate 15 -burst 5 -maxage 3600`.
Arti praktisnya:

- Query yang sama (graph sama, binary sama) dijawab `304` kalau klien mengirim
  `If-None-Match`: ~0,2 ms, nyaris tidak memakai slot. `Cache-Control` membuat
  app/browser menyimpannya selama `-maxage` detik.
- Rute yang tidak selesai dalam `-timeout` dijawab `504` dan slotnya dikembalikan
  ke antrean, jadi satu permintaan rute 148 km tidak menyandera server.
- Per IP: 15 permintaan/menit (burst 5) -> `429` + `Retry-After`.
- `curl localhost:8080/metrics` untuk dashboard.

### Menaruh di belakang Caddy (disarankan)

Sekarang port 8080 dipublikasikan langsung ke LAN, jadi **jangan** pakai
`-trust-proxy`: `X-Forwarded-For` bisa dipalsukan dan rate limit bisa dilewati.
Kalau mau pindah ke belakang Caddy:

```yaml
# deploy/docker-compose.yml
    ports:
      - "127.0.0.1:8080:8080"   # hanya proxy di host yang bisa menghubungi
    command:
      # ... flag lama ...
      - -trust-proxy            # ambil IP dari X-Forwarded-For
```

```caddyfile
route.example.com {
    reverse_proxy 127.0.0.1:8080
    encode gzip
}
```

Setelah itu `curl http://192.168.1.219:8080` memang tidak menjawab lagi (itu
tujuannya); akses lewat nama domain Caddy, dan Postman diarahkan ke sana.
