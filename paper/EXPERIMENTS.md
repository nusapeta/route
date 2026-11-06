# Yang masih kurang sebelum paper ini layak submit

Urut berdasarkan dampak ke kredibilitas.

## E1. Baseline OSRM di mesin yang sama (WAJIB)

Paper sekarang **tidak** punya pembanding terkontrol. Tanpa ini, klaim
"lebih ringan" hanya sebanding dengan angka yang dilaporkan orang lain.

```bash
# perlu user masuk grup docker (atau sudo)
sudo usermod -aG docker $USER   # logout/login

mkdir -p baseline && cd baseline
wget https://download.geofabrik.de/asia/indonesia/java-latest.osm.pbf
docker run -t -v $PWD:/data osrm/osrm-backend osrm-extract -p /opt/car.lua /data/java-latest.osm.pbf
docker run -t -v $PWD:/data osrm/osrm-backend osrm-partition /data/java-latest.osrm
docker run -t -v $PWD:/data osrm/osrm-backend osrm-customize /data/java-latest.osrm
/usr/bin/time -v docker run -d --name osrm -p 5001:5000 -v $PWD:/data \
  osrm/osrm-backend osrm-routed --algorithm mld /data/java-latest.osrm
```

Yang harus dicatat: durasi tiap tahap, peak RSS tiap tahap (`docker stats`),
ukuran direktori `.osrm`, RSS `osrm-routed` setelah start dan setelah workload.

Harness pembanding (pakai pasangan yang sama):

```bash
# pasangan sama, OSRM order lon,lat
python3 - <<'EOF'
import csv, urllib.request, json, time
pairs = list(csv.DictReader(open('paper/results/pairs-java.csv')))
lat = []
for p in pairs:
    u = f"http://127.0.0.1:5001/route/v1/driving/{p['from_lon']},{p['from_lat']};{p['to_lon']},{p['to_lat']}?overview=false"
    t0 = time.perf_counter(); json.load(urllib.request.urlopen(u)); lat.append((time.perf_counter()-t0)*1000)
lat.sort()
print(f"n={len(lat)} p50={lat[len(lat)//2]:.1f}ms p95={lat[int(len(lat)*.95)]:.1f}ms")
EOF
```

Kriteria lolos: tabel latency + memori untuk kedua engine, pada hardware & data
identik, dengan jumlah query sukses yang sama.

## E2. Validasi akurasi terhadap referensi (WAJIB untuk klaim "akurat")

Butuh Google Routes API key + billing (atau Mapbox/HERE sebagai pembanding kedua).

Rencana:
1. Ambil 300–500 pasangan dari `pairs-java.csv` (semua bucket ≤50 km).
2. Ambil `distanceMeters` + polyline dari reference API.
3. Hitung: selisih jarak relatif, dan geometri (mean distance between
   corresponding points setelah resampling, atau symmetric Hausdorff).
4. Laporkan median, p95, plus daftar kasus terburuk (biasanya: turn restriction,
   jalan tol/gate, jalan baru).

Target yang bisa dijanjikan di paper: **median selisih jarak <5 %, p95 <12 %**.
Kalau tidak tercapai, perbaiki dulu (E3/E4) sebelum submit.

## E3. Turn restriction — SELESAI (kode + pengukuran)

Implementasi: state A* = edge (bukan node), tabel sparse pasangan terlarang di
format v2 (`Ban`) dan pasangan yang diizinkan untuk `only_*` (`Only`).
Ekstraksi: `internal/extract/restriction.go`. Uji kepatuhan: `cmd/rid-restcheck`.

Hasil Jawa (lihat `RESULTS.md` §7): 4.653 relasi → 4.203 usable, 3.794 pasangan
terlarang + 155 aturan `only_*` (32 KB). Kepatuhan: 0 pelanggaran dari 3.564
sampel (dengan restriction) vs 95,79% pelanggaran tanpa restriction.
Sisa 3,17% sampel jatuh ke fallback tanpa restriction (data OSM/rantai via-way).

Sisa pekerjaan untuk versi jurnal: `restriction:conditional` (berbasis waktu)
dan via-way yang dilarang konservatif (butuh state machine / edge splitting).

## E4. Snap ke edge, bukan node

Proyeksi titik ke segmen terdekat (grid edge index), lalu split logis di query
time. Mengurangi error endpoint sampai ratusan meter tempat node jarang.

## E5. Build nasional (T1) terukur, bukan estimasi

Dua jalur:
- mesin 16 GB / GitHub Actions runner: build `indonesia-latest.osm.pbf`, catat
  waktu + peak RSS + ukuran graph;
- atau implementasi T3 (streaming builder: sort id eksternal + CSR ditulis per
  blok) supaya muat di 8 GB. Tambahkan flag `-maxmem`.

Yang dilaporkan: apakah graph nasional ≈ 2× Java (node/edge/disk) dan apakah
p50/p95 latency tidak berubah (search terbatas radius, jadi seharusnya tidak).

## E6. Statistik & reproduksibilitas

- Ulangi benchmark 5×, laporkan median antar-run (bukan satu run).
- Pin CPU (`taskset -c 0`) dan matikan turbo kalau memungkinkan.
- Laporkan ukuran sampel per bucket; p99 dengan n<100 sebaiknya tidak diklaim.
- Simpan CSV mentah (sudah ada di `paper/results/`), sertakan skrip harness.
- Ukur juga `page faults` (`/usr/bin/time -v`) atau `smaps_rollup` untuk
  memisahkan RSS file-backed vs anonim.

## E7. Generalisasi

Minimal satu pulau lagi (Sulawesi atau Kalimantan) untuk menunjukkan hasil tidak
khas Jawa, plus satu kota padat (Bandung/Surabaya) untuk bucket 10–25 km.

## Catatan venue

- Preprint: arXiv (cs.DC / cs.PF) — paling cepat.
- Workshop: ACM SIGSPATIAL IWCTS (short paper, cocok untuk studi kasus).
- Jurnal lokal: JUTI, SINERGI, atau IJECE (versi diperluas dengan E1–E6).

Estimasi: E1 + E2 + E6 cukup untuk short paper (2–3 minggu, mayoritas waktu di
mengumpulkan ground truth). E3 + E4 + E5 masuk versi jurnal.
