# Roadmap routeid

**North star:** alternatif OSRM / Google Routes yang paling ringan —
router jalan Indonesia yang memakai puluhan MB, bukan gigabyte, dan tetap
menjawab rute <=50 km di bawah setengah detik di perangkat murah.

## Non-goals (sengaja ditolak)

Menolak hal-hal ini adalah bagian dari strategi, bukan kekurangan sementara:

- **Traffic / ETA real-time.** Butuh data live + penyimpanan time-dependent
  graph; membunuh budget memori dan membuat hasil tak dapat direproduksi.
- **Cakupan dunia.** Fokus Indonesia; format graph boleh mengasumsikan tabel
  kecepatan per kelas jalan lokal.
- **Turn-by-turn instruction.** Itu pekerjaan NLP atas nama jalan; layanan
  hosted lebih tepat. Kalau nanti dibutuhkan, ditambahkan sebagai modul terpisah
  yang membaca artefak yang sama.
- **Multi-modality penuh** (sepeda, angkutan umum). Motor masuk karena
  relevansi pasar Indonesia; sisanya tidak.
- **CH/MLD penuh.** Preprocessing puluhan menit dan artefak berlipat bertentangan
  dengan identitas "ringan". Kalau rute panjang dibutuhkan, pakai dekomposisi
  dua tingkat, bukan contraction penuh.

## Budget "paling ringan" (angka yang mengikat)

Setiap perubahan harus lulus tabel ini. Jika sebuah fitur melewati budget, ia
butuh argumen eksplisit dan milestone sendiri.

| metrik | sekarang (terukur) | target M3 | batas keras |
|---|---|---|---|
| RSS idle (2 region termuat) | 15 MB | <= 20 MB | 32 MB |
| Binary statis | 6,6 MB | <= 8 MB | 12 MB |
| Image container | 23 MB | <= 25 MB | 40 MB |
| Start sampai `/healthz` 200 | 10 ms | <= 25 ms | 100 ms |
| Preprocess per pulau | 58 s | <= 90 s | 5 menit |
| RSS @ beban 42 km, `-conc 4` | 558 MB | <= 350 MB | 700 MB |
| Throughput 42 km (i3-6100T) | 10,9 RPS | >= 20 RPS | - |
| Densitas | 19,5 RPS/GB | >= 45 RPS/GB | - |
| p95 klien 42 km, `-conc 4` | 439 ms | <= 400 ms | 800 ms |
| Galat ujung rute (snap) | belum diukur | p50 <= 20 m, p95 <= 60 m | p95 150 m |
| Pelanggaran turn restriction | 0 (3,17% fallback) | 0 (fallback <= 1%) | 0 |

"Densitas" = RPS / RSS, metrik utama proyek ini: satu GB RAM harus menampung
sebanyak mungkin rute per detik.

## Milestone

### M1 — Bisa dipercaya — SELESAI

Tujuan: artefak bisa diaudit dan regresi ketahuan otomatis. Tanpa ini, angka
performa tidak bisa dibandingkan antar build.

- [x] Metadata graph format **v3**: SHA-256 pbf + waktu build di header, blob
      `key=value` di akhir file (`source`, `pbf_sha256`, `pbf_bytes`,
      `extractor`, `profile_sha256`, `order`, `grid_step`, `restrictions`).
      Terbaca di `/healthz`, `/version`, dan `rid-graphinfo`. v1/v2 tetap dimuat.
- [x] Build info di binary via `-ldflags` (`main.commit`, `main.buildTime`,
      `extract.Version`), otomatis dari `make build` / `make static`.
- [x] Golden test 48 pasangan pada kota sintetis 30x30, plus round-trip
      provenance dan uji kompatibilitas v2.
- [x] Biaya diukur: hash pass menambah ~0,3 s dari 21,2 s total ekstraksi
      Sumatra (peak 1.661 MB, sama seperti sebelumnya); blob metadata 288 byte.

**Terbukti:** `format: v3`, `pbf_sha256: dcbc563f…` di `/healthz`; mengubah satu
kecepatan di test menggagalkan 3 pasangan (harness tidak vakum).

### M2 — Akurasi ujung rute (2-3 hari)

Tujuan: pin user jatuh di jalan, bukan di node terdekat yang mungkin 300 m jauhnya.

- [ ] Snap ke ruas (proyeksi tegak lurus ke segmen), pseudo-node di ujung.
- [ ] Harness `rid-snapcheck`: 200 pasangan, laporkan p50/p95 galat ujung (meter).
- [ ] Simpan hasilnya sebagai bagian E2 (validasi akurasi).

**Selesai bila:** p50 <= 20 m dan p95 <= 60 m pada data uji, dan hasil rute
tidak berubah untuk pasangan yang sudah tepat di node.

### M3 — Naikkan densitas 2x (3-5 hari)

Tujuan: satu box kecil menampung ~1.200 user (1 rute/menit), bukan 600.

- [ ] ALT: 16 landmark, `max(|d(l,v)-d(l,t)|)`, tetap admissible, kompatibel
      dengan eps yang ada. Tambah 16 x 4 B/node = 16 MB untuk Java.
- [ ] Workspace A*: alokasi awal dari perkiraan jarak, hindari grow berulang;
      `GOMEMLIMIT` sebagai plafon.
- [ ] Kurangi RSS beban: pertimbangkan bidirectional agar frontier lebih kecil.

**Selesai bila:** >= 20 RPS @42 km di i3, RSS beban <= 350 MB, dan jarak rute
identik dengan baseline (dibuktikan golden test M1).

### M4 — Siap dipakai app (2-3 hari)

- [ ] Timeout per request (`http.TimeoutHandler` 10 s) + `context` yang
      membatalkan pencarian A* di tengah jalan.
- [ ] Rate limit per IP (15 req/menit) dan bind `127.0.0.1` di belakang Caddy.
- [ ] `/metrics` (Prometheus): request, bucket latency, ekspansi, fallback.
- [ ] `ETag` + `Cache-Control` untuk query identik (app mengulang saat rotasi).
- [ ] `points&simplify=8` jadi format yang didokumentasikan untuk Flutter.

**Selesai bila:** tidak ada request yang bisa menahan slot >10 s, dan dashboard
menampilkan RPS/p95/fallback.

### M5 — Cakupan (3-4 hari)

- [ ] Profil motor (tabel kecepatan + `motorcycle=no`, `service` lebih permisif).
- [ ] Semua pulau besar: Kalimantan, Sulawesi, Bali-Nusra (~1 menit build masing).
- [ ] Konektor ferry (Merak-Bakauheni, Ketapang-Gilimanuk, dsb) sebagai sub-graph
      sehingga rute lintas selat tidak lagi `422`.
- [ ] Kurangi artefak: turunkan `Time` dari `Len` + kelas jalan (hemat ~187 MB di
      Java), varint untuk `GO`/`GL`.

**Selesai bila:** Jakarta -> Medan mengembalikan rute (dengan segmen ferry
ditandai), dan graph Java <= 800 MB.

### M6 — Bukti (paper)

- [ ] E1 baseline OSRM di mesin sama (`paper/EXPERIMENTS.md`).
- [ ] E2 ground truth 60-200 pasangan (`paper/COMPARISON.md` §4).
- [ ] Tabel positioning diisi angka nyata, bukan "documented".

**Selesai bila:** `paper/COMPARISON.md` tidak lagi punya baris berstatus
"belum diukur" untuk metrik inti.

## Urutan & ketergantungan

```
M1 (metadata, golden test)
 └─> M2 (snap)  ─┐
 └─> M3 (ALT)   ─┼─> M4 (produksi) ─> M5 (cakupan) ─> M6 (paper)
                 └─ M1 diperlukan agar M2/M3 bisa dibuktikan tidak meregresi
```

M1 wajib lebih dulu: tanpa golden test, M2 dan M3 mengubah rute tanpa cara
mengetahui apakah perubahannya benar.

## Risiko

| risiko | dampak | mitigasi |
|---|---|---|
| ALT memperbesar artefak 16 MB/pulau | bertentangan dengan "ringan" | 4 landmark dulu, ukur trade-off; alternatif: landmark per sel grid |
| Proyeksi ke ruas menambah seksi grid | build lebih lambat, format berubah | naikkan versi format, arsipkan artefak lama |
| Ferry connector melanggar aturan "satu region per rute" | kompleksitas | batasi hanya pasangan selat yang terdefinisi |
| Terlalu banyak fitur -> keluar dari identitas ringan | kehilangan pembeda | setiap fitur diuji terhadap tabel budget di atas |

## Catatan yang belum jadi milestone

- **Satu graph nasional** (1,66 GB pbf, ~5 GB RAM) hanya masuk akal dengan
  streaming extractor yang menulis langsung ke file; multi-region per pulau
  sudah memberi hasil yang sama dengan memori jauh lebih kecil.
- **Hierarki dua tingkat** (highway/core) baru dipertimbangkan jika ada kebutuhan
  rute >60 km atau <20 ms; CH/MLD penuh tetap ditolak.

## Cara mengukur (jangan improvisasi)

- Throughput & latensi: `scripts/loadtest.py` (kelas rute 7/15/42/148 km).
- Memori: `/proc/<pid>/status` — `RssAnon` vs `RssFile`, bukan `ps rss`.
- Regresi rute: golden 50 pasangan dari M1.
- Turn restriction: `rid-restcheck`.
- Semua angka yang masuk paper harus punya perintah reproduksi di
  `paper/RESULTS.md`, dan statusnya dicatat di `paper/COMPARISON.md`.
