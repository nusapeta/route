# Deploy di homelab (Jawa + Sumatra)

Tiga cara: Docker Compose (paling cepat), systemd (tanpa Docker), atau
Compose + Cloudflare Tunnel untuk akses domain saja. Semua memakai graph yang
sudah dibangun di host; image tidak pernah memuat data 1,5 GB.

## 0. Bangun dan refresh graph (di host)

Build jauh lebih berat dari run: puncak ~2,5 GB (Java) dan ~1,4 GB (Sumatra).
Kalau RAM pas-pasan, batasi Go: `GOMEMLIMIT=1800MiB make java`.

```bash
make refresh REGION=java
make refresh REGION=sumatra
ls -lh data/*.graph          # java ~693 MB, sumatra ~347 MB (v4, mobil)
```

`make refresh` mengunduh PBF terbaru, membangun profil mobil dan motor,
menjalankan `rid-tagstats`, golden suite, dan A/B singkat. Jadwalnya Minggu
pertama setiap bulan pukul 03.00 WIB oleh operator homelab (Fauzan Arif Sani);
laporan ada di `data/<region>-graphinfo.txt` dan `data/<region>-ab.txt`.
Ganti graph yang dipakai container hanya setelah `/healthz` graph baru
menampilkan `profile`, `pbf_sha256`, dan `built_at` yang diharapkan.

Build manual masih bisa dipakai:
```bash
make build
./bin/rid-extract -pbf data/java-latest.osm.pbf    -out data/java.graph
./bin/rid-extract -pbf data/sumatra-latest.osm.pbf -out data/sumatra.graph
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

Kirim juga source kalau image dibangun di host (Dockerfile build dari konteks):

```bash
rsync -az --delete \
  --exclude '.git/' --exclude 'data/' --exclude 'bin/' --exclude 'paper/' \
  --exclude 'deploy/.env' --exclude '*.graph' --exclude '*.osm.pbf' \
  --exclude '*.geojson' --exclude '*.bak*' \
  ./ host:~/Projects/routeid/
```

`--delete` menghapus apa pun yang tidak ada di mesin pengembang, termasuk
cadangan lokal `deploy/*.bak-*` di host, jadi kecualikan `*.bak*` sebelum
menjalankannya. `.env`, `data/`, dan `bin/` hanya ada di host.

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

## 1c. Dua profil, satu base URL

Mobil dan motor memakai satu artefak graf v5 per region, tetapi satu proses
hanya melayani satu profil (`-profile car|motor`). Karena itu compose
menjalankan tiga container inti dari image yang sama:

| container | port | isi |
|---|---|---|
| `routeid-gateway` | `127.0.0.1:8080` (loopback) | proxy pemilih profil |
| `routeid` | `127.0.0.1:18080` (loopback) | `java`, `sumatra`, `nusa-tenggara` (mobil) |
| `routeid-motor` | `127.0.0.1:18081` (loopback) | `java`, `sumatra`, `nusa-tenggara` dengan `-profile motor` |
| `routeid-cloudflared` | tanpa port | opsional, profil compose `cloudflare` |

Tidak ada port yang mendengarkan di `0.0.0.0`; akses dari luar host hanya lewat
Cloudflare Tunnel (bagian 1d).

Klien memakai satu base URL dan memilih profil lewat parameter:

```bash
curl '127.0.0.1:8080/route?from=-6.1754,106.8272&to=-6.1900,106.8300'                    # mobil
curl '127.0.0.1:8080/route?from=-6.1754,106.8272&to=-6.1900,106.8300&profile=motor'      # motor
curl '127.0.0.1:8080/route?from=3.5952,98.6722&to=3.6424,98.8853&profile=motor'          # motor Sumatra
curl '127.0.0.1:8080/route?from=-8.6563,115.2167&to=-8.7950,115.2280&profile=motor'      # motor Bali
curl -s 127.0.0.1:8080/healthz | head -c 200        # kedua profil sekaligus (503 bila satu mati)
curl -s '127.0.0.1:8080/healthz?profile=motor'      # satu profil saja
```

`profile=mobil|motor` berlaku untuk `/route` dan `/version`; parameter itu
dicopot sebelum diteruskan, jadi yang dicatat upstream adalah request apa
adanya. Nilai lain dijawab `400`, bukan diam-diam memakai default. Respons
mendapat header `X-Route-Profile` supaya klien tahu profil mana yang melayani.
Tanpa parameter, profil mobil yang dipakai (perilaku lama port 8080).
`/metrics` sengaja tidak diteruskan gateway publik; scrape upstream langsung
(bagian 3).

```bash
make static                                   # bin/static/rid-extract + rid-gateway
for r in java sumatra nusa-tenggara; do
  make graph REGION=$r                        # satu artefak: mobil + motor
done
cd deploy && docker compose up -d             # gateway ikut dibuat
curl -s 127.0.0.1:8080/healthz | head -c 300
```

Sejak format v5 satu file per region memuat kedua profil; `routeid-motor`
membuka file yang sama dengan `-profile motor`. Angka lama (dua artefak
terpisah, 2026-11-10) tetap dicatat di `docs/PLAN-2026-11-10.md`; ukuran v5
diukur ulang setelah rollout.

Satu proses memuat ketiganya. Setelah rollout v5 (2026-11-12) dan enam rute
campuran, container mobil memakai 76 MB (`docker stats`, `VmRSS` 135 MB,
`RssAnon` 65 MB termasuk tabel A* 41 MB) dan container motor 26 MB
(`VmRSS` 37 MB, `RssAnon` 17 MB, tabel 11 MB). Kalau hanya mobil yang dipakai,
layanan `routeid-motor` boleh dihapus dari compose tanpa mengubah yang lain;
gateway lalu melaporkan profil motor tidak sehat.

Kedua upstream memakai `-trust-proxy` karena rate limit harus memakai IP klien,
bukan IP gateway. Gateway menimpa `X-Forwarded-For`/`X-Real-IP` (bukan
menambahkan). Dengan `-trust-proxy`, gateway mengambil `CF-Connecting-IP` dari
Cloudflare Tunnel atau hop pertama `X-Forwarded-For`; tanpa flag itu, alamat
peer yang dipakai. Port upstream yang loopback-only membuat spoofing butuh
akses ke host.

## 1d. Cloudflare Tunnel (domain saja)

Tujuan: satu-satunya pintu masuk publik adalah `https://route.arifsani.xyz`.
Port 8080 tetap loopback; tidak ada IP/LAN yang bisa dipanggil dari internet.

### Di Cloudflare

1. **Networking > Tunnels > Create Tunnel**, beri nama `routeid`.
2. Pilih lingkungan **Docker**. Salin token dari perintah
   `cloudflared tunnel --no-autoupdate run --token ...`.
3. Di tunnel itu, **Routes > Add route > Published application**:
   - Hostname: `route`
   - Domain: `arifsani.xyz`
   - Service URL: `http://gateway:8080`
   - Jangan memakai `http://127.0.0.1:8080`: itu loopback container
     `cloudflared`, bukan gateway.
4. Simpan. Cloudflare membuat CNAME `route.arifsani.xyz` yang proxied ke
   `<tunnel-id>.cfargotunnel.com`; jangan matikan proxy/orange cloud.
5. SSL/TLS zona: **Automatic SSL/TLS** (atau Full). Tunnel sudah terenkripsi
   dari edge ke `cloudflared`; origin HTTP internal tidak butuh sertifikat.
   Nyalakan **Always Use HTTPS**.
6. Opsional: **Security > WAF > Custom rules** blokir
   `http.request.uri.path eq "/metrics"` sebagai lapisan kedua. Gateway sudah
   mengembalikan `404` untuk path itu (`-block-metrics`), jadi aturan ini tidak
   wajib.

### Di host

```bash
cd deploy
cp .env.example .env
$EDITOR .env                 # isi CLOUDFLARE_TUNNEL_TOKEN=...
docker compose --profile cloudflare up -d --build
docker compose --profile cloudflare ps
docker logs --tail 30 routeid-cloudflared   # cari "Registered tunnel connection"
```

Token hanya ada di `deploy/.env`, yang di-ignore git. Jangan menaruhnya di
`docker-compose.yml` atau command line shell yang tersimpan di history.

### Kalau cloudflared sudah berjalan

Homelab memakai container `cloudflared-tunnel` dari 1Panel, jadi service
`cloudflared` di compose tidak perlu dinyalakan. Yang wajib: cloudflared dan
gateway berada di network Docker yang sama. Override
`docker-compose.homelab.yml` menambahkan gateway ke `1panel-network` dengan
alias `gateway`:

```bash
docker inspect cloudflared-tunnel \
  --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}'
docker compose -f docker-compose.yml -f docker-compose.homelab.yml up -d
```

Di homelab, `deploy/.env` memuat
`COMPOSE_FILE=docker-compose.yml:docker-compose.homelab.yml`, jadi
`docker compose up -d` biasa juga otomatis memakai override. Jangan hapus baris
itu, dan jangan menimpa `.env` dengan `cp .env.example .env` di host ini.

Jangan memakai `--profile cloudflare` di host itu: connector kedua akan
menerima seluruh ingress tunnel, tetapi hanya bisa me-resolve origin yang ada
di network compose ini, bukan origin 1Panel lainnya. Kalau network berbeda,
gejalanya `502` dan log cloudflared berisi
`lookup gateway ... no such host`.

### Verifikasi

```bash
curl -sS https://route.arifsani.xyz/healthz | head -c 300; echo
curl -sS https://route.arifsani.xyz/version; echo
curl -sS 'https://route.arifsani.xyz/route?from=-6.2088,106.8456&to=-6.2297,106.8166&format=polyline' | head -c 120; echo
curl -sS -o /dev/null -w 'metrics: %{http_code}\n' https://route.arifsani.xyz/metrics   # 404
lan_ip=$(ip -4 addr show scope global | awk '/inet /{print $2; exit}' | cut -d/ -f1)
curl -sS -o /dev/null -w 'LAN: %{http_code}\n' "http://$lan_ip:8080/healthz" || true
METRICS_BASE=http://127.0.0.1:18080 scripts/smoke.sh https://route.arifsani.xyz
```

`metrics: 404` dan `LAN` gagal/connection refused adalah hasil yang benar.
Kalau `cloudflared` restart terus: token salah/kedaluwarsa, atau host tidak bisa
keluar ke Cloudflare port `7844`. Kalau domain 502: untuk tunnel compose cek
`docker compose --profile cloudflare ps`; untuk tunnel 1Panel cek
`docker logs --tail 50 cloudflared-tunnel`. Log
`lookup gateway ... no such host` berarti cloudflared dan gateway tidak berada
di network Docker yang sama; pakai `docker-compose.homelab.yml` di atas.

Rate limit upstream tetap per klien karena gateway membaca `CF-Connecting-IP`
dan menimpa `X-Forwarded-For` sebelum meneruskan. Tanpa `-trust-proxy`, semua
pengunjung akan berbagi satu bucket milik container `cloudflared`.

Server ini tanpa autentikasi. Kalau tidak ingin API terbuka untuk siapa saja,
tambahkan **Cloudflare Access** pada hostname yang sama; aplikasi tidak perlu
diubah.

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
| beban kontrak 100 km, `-conc 4 -workspace-mb 256` (Ryzen 5 5600G) | 1,02-1,10 GB (`anon` 343-425 MB, tabel 126-201 MB) |
| homelab i3-6100T, v5, 6 rute campuran (56 km, ferry, lintas region) | `routeid` 76 MB (`anon` 65 MB), `routeid-motor` 26 MB (`anon` 17 MB) |

CPU 1 core penuh selama satu query: 128 ms (15 km), 3,6 s (148 km),
1,7 s (578 km). Disk 1,2 GB untuk tiga region v5, wajib SSD (halaman mmap
dibaca saat disentuh).
Startup 10 ms.

Untuk mesin kecil: `-conc 1` (memori ~4x lebih hemat, query diserialkan) dan
`GOMEMLIMIT=512MiB`.

Dua flag yang dipakai container sejak build `8e2dc11`:

- `-workspace-mb 256`: anggaran total tabel A*, dibagi rata ke `-conc` slot
  (12 B/slot, jadi 64 MB/slot pada `-conc 4`). Tabel mulai 0,75 MB, tumbuh saat
  rute panjang, dan menyusut balik bila kapasitas > 8x pemakaian. Bila anggaran
  habis, klien menerima `422 search space exhausted (route too long)`, bukan
  OOM. `0` = tanpa anggaran eksplisit (plafon lama 384 MB/slot). Pada kontrak
  100 km tabel terukur 126-201 MB dari 256 MB, dan menaikkan anggaran ke
  512/2048 tidak mengubah pasangan yang gagal (yang habis anggaran ekspansi
  A*, bukan tabel), jadi 256 tetap dipakai (`paper/RESULTS.md` §19).
- `-max-route-km 100`: kontrak aplikasi, rute di atas 100 km (garis lurus lebih
  dulu, lalu jarak tempuh) ditolak `422`. Segmen <= 50 km tetap p50 24-73 ms dan
  p95 246-705 ms (`paper/RESULTS.md` §16); bucket 50-80 km p50 430-542 ms,
  p95 1,40-1,74 s (§19). Rute > 100 km tidak lagi dilayani tanpa menaikkan flag.

Ukur sendiri sesudah beban:

```bash
# Gateway publik menolak /metrics; scrape proses upstream langsung.
scripts/memcheck.sh http://127.0.0.1:18080           # mobil
scripts/memcheck.sh http://127.0.0.1:18081           # motor
curl -s http://127.0.0.1:18080/metrics | grep ^routeid_workspace_
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

Gateway sudah membatasi per IP (15/menit, burst 5). Kalau butuh batas
kasar tambahan di reverse proxy, kuncinya harus IP klien, bukan IP proxy:

```
routeid.lan {
    rate_limit {
        zone routeid { key {http.request.header.CF-Connecting-IP} events 30 window 1m }
    }
    reverse_proxy 127.0.0.1:8080 {
        transport http { response_header_timeout 30s }
    }
}
```

`{remote_host}` di belakang Cloudflare Tunnel adalah IP container
`cloudflared`, jadi semua pengunjung akan berbagi satu bucket. Gateway sendiri
sudah membaca `CF-Connecting-IP` lewat `-trust-proxy`.

`GET /` dan `/assets/*` dilayani gateway untuk demo peta. Gateway compose
memakai `-block-metrics`, jadi `https://route.arifsani.xyz/metrics` selalu
`404`; dashboard internal membaca `127.0.0.1:18080/metrics` dan
`127.0.0.1:18081/metrics` langsung. Jangan mengganti flag itu di deployment
publik.

## 4. Keamanan

Server ini **tanpa autentikasi**, `Access-Control-Allow-Origin: *`, dan rate
limit per IP aktif secara default (`-rate 15 -burst 5`, lihat M4 di bawah).
Untuk homelab:

- bind ke loopback (`127.0.0.1:8080`) dan pakai Cloudflare Tunnel; jangan
  publish port 8080 ke `0.0.0.0` atau router.
- Cloudflare Tunnel sudah memberi TLS di edge; tidak perlu Caddy hanya untuk
  HTTPS. Kalau tetap memakai reverse proxy lokal, pastikan proxy menimpa
  `X-Forwarded-For`.
- jangan expose ke internet tanpa auth + rate limit: satu query rute panjang
  memakan 1 core selama beberapa detik. Tambahkan Cloudflare Access bila API
  tidak boleh publik.

## 5. Update data OSM

```bash
curl -O https://download.geofabrik.de/asia/indonesia/java-latest.osm.pbf
make build && ./bin/rid-extract -pbf java-latest.osm.pbf -out data/java.graph
docker compose restart      # atau: sudo systemctl restart routeid
```

Untuk update tanpa downtime: bangun ke nama lain (`java-new.graph`), lalu
jalankan dua server di port berbeda dan alihkan proxy.

## M4: cache, batas waktu, batas laju, metrics

Compose sekarang meneruskan
`-timeout 10s -budget 6s -max-expansions 4000000 -rate 15 -burst 5 -maxage 3600`.
Arti praktisnya:

- Query yang sama (graph sama, binary sama) dijawab `304` kalau klien mengirim
  `If-None-Match`: ~0,2 ms, nyaris tidak memakai slot. `Cache-Control` membuat
  app/browser menyimpannya selama `-maxage` detik.
- Rute yang tidak selesai dalam `-budget` (6 s) dijawab
  `422 search budget 6s exceeded`, bukan `504`: di i3-6100T anggaran ekspansi
  lama (6 juta) baru habis pada 12,8 s, jadi batas waktu keras selalu menang dan
  statusnya berubah-ubah mengikuti beban. Slot dikembalikan ke antrean, jadi satu
  permintaan rute 148 km tidak menyandera server.
- Per IP: 15 permintaan/menit (burst 5) -> `429` + `Retry-After`.
- `curl 127.0.0.1:18080/metrics` (mobil) atau `:18081` (motor) untuk dashboard.
  Gateway publik memakai `-block-metrics` dan menjawab `404`.

### Menaruh Caddy di depan gateway

Deployment homelab tidak memakai Caddy: TLS dan nama domain datang dari
Cloudflare Tunnel (`https://route.arifsani.xyz`). Bagian ini hanya untuk host
yang belum punya tunnel dan butuh TLS lokal. Gateway sudah menjadi pintu depan,
jadi Caddy hanya perlu meneruskan ke sana:

```caddyfile
route.example.com {
    reverse_proxy 127.0.0.1:8080
    encode gzip
}
```

Compose mengaktifkan `-trust-proxy`, jadi gateway memakai `CF-Connecting-IP`
(Cloudflare Tunnel) atau hop pertama `X-Forwarded-For`. Kalau Caddy dipakai,
pastikan Caddy menimpa header itu dengan IP klien; kalau tidak, semua klien
berbagi bucket Caddy. Port upstream 18080/18081 tetap loopback, jadi tidak ada
jalur yang melewati gateway.
