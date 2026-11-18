# Backlog

Hanya pekerjaan yang **belum selesai**. Riwayat hasil ada di
[`ROADMAP.md`](ROADMAP.md) dan [`../paper/RESULTS.md`](../paper/RESULTS.md);
jangan menyalin catatan milestone ke sini.

Prioritas: **P1** dibutuhkan untuk klaim/operasi, **P2** penting tetapi bisa
ditunda, **P3** riset/opsional.

## P1 — korektnes data

### 16b. Restriction conditional (diputuskan: tidak didukung, NUSA-14)

**Masalah.** Relasi yang hanya punya `restriction:conditional` (jam sekolah,
jam pasar, ganjil-genap) dihitung sebagai `conditional` lalu dibuang. Graph
statis tidak bisa mengevaluasi waktu.

**Pilihan.**

1. Dokumentasikan sebagai batasan (status sekarang).
2. Simpan jendela waktu per ban di format v5 dan evaluasi jam lokal request.
3. Terapkan larangan tanpa syarat (konservatif; bisa menutup jalan di luar jam).

**Lulus bila.** Pilihan eksplisit + angka dampak pada 30 pasangan yang
melintasi jalan dengan conditional. Jangan menambah format sebelum ada angka.

**Keputusan NUSA-14.** Pilihan 1 dipakai: tidak didukung dengan reason eksplisit.
Graph v5 tidak punya waktu berangkat atau zona waktu, tidak ada fixture jendela
berlaku, dan v6 menuntut pembaruan anggaran kolom 07 ENG lebih dulu. Relasi tetap
dihitung sebagai `conditional` lalu dibuang; 0 aturan tambahan, 0 byte tambahan,
fallback tidak berubah. Detail dan angka ada di `paper/RESULTS.md` §14e dan
`docs/API.md`. Angka dampak 30 pasangan belum tersedia, jadi opsi 2 tertahan.

### 14b. Jadwal ferry

**Masalah.** `duration` OSM sering kosong. Merak–Bakauheni terukur 1,45 jam,
tetapi banyak lintasan kecil jatuh ke asumsi 20 km/jam.

**Rencana.** Tabel statis per lintasan (waktu, frekuensi) untuk lintasan yang
benar-benar dipakai; jangan menambahkan jadwal real-time.

**Lulus bila.** Selisih ETA ferry pada 10 lintasan utama < 20% dibanding jadwal
resmi, dan respons menandai sumber waktu.

### 13b. Profil motor: sisa

- `motorcycle:conditional`, `access:conditional` belum dievaluasi.
- Bobot gang: `footway` masih 10 km/jam untuk semua; turunkan dari
  `surface`/`smoothness` bila datanya cukup.
- Motor belum punya perilaku menerobos kemacetan; traffic tidak dimodelkan.

**Lulus bila.** Setiap perubahan punya A/B rute dan tidak menambah fallback
restriction.

## P2 — performa dan operasi

### 13c. Budget ekspansi / hierarchy

**Masalah.** Tanpa hierarchy, koridor 80–100 km bisa memakai 2–3 juta ekspansi
dan menyentuh budget 6 s. `-workspace-mb` bukan penyebab: menaikkannya tidak
memindahkan pasangan dari 422 ke 200.

**Rencana.** Ukur demand rute >100 km lebih dulu. Kalau ada, buat hierarchy dua
tingkat (highway graph lalu detail di sekitar ujung), bukan CH/MLD penuh.
Pertahankan `-max-expansions` sebagai plafon deterministik.

**Lulus bila.** Rute 100 km p95 <1 s di i3 tanpa menaikkan RSS >1,5 GB, dan
golden test jarak/waktu tetap lulus.

### 18. HTTPS-only

**Masalah.** `http://route.example.com/healthz` masih menjawab `200`; belum
redirect ke HTTPS. Ini setelan Cloudflare, bukan kode.

**Rencana.** Aktifkan **Always Use HTTPS** di Cloudflare (SSL/TLS → Edge
Certificates). Verifikasi `http://` → `301/308` ke `https://` dan API tetap
bisa diakses.

**Lulus bila.** HTTP plaintext tidak melayani route/healthz.

### Image size

**Masalah.** Image multi-stage terukur 36,7 MB; target lama 25 MB tidak
tercapai. Varian prebuilt belum diukur ulang.

**Rencana.** Ukur kontribusi layer, hapus ca-certificates jika tidak perlu,
pertimbangkan `scratch`/distroless untuk server. Jangan mengubah target tanpa
pengukuran.

**Lulus bila.** Image ≤32 MB atau keputusan eksplisit untuk menerima ukuran
sekarang.

## P3 — riset / ditunda

### 13d. Radix heap, 16-bit tile-local, MLD

Ditunda karena benturan golden test, bukan karena waktu:

- **Radix heap** mengubah urutan tie dan `seq_hash`; weighted A* tidak konsisten.
- **16-bit tile-local** butuh renumber node; tabel pemetaan menghapus penghematan.
- **MLD** memberi jarak/waktu sama tetapi jalur berbeda pada biaya identik;
  manfaat produk nol selama cap 100 km.

Kalau gate golden dilonggarkan, urutan kerja: MLD (uji jarak/waktu), radix heap,
lalu format tile-local.

### E2. Ground truth

Tidak dijalankan (D1, 2026-11-10). Tanpa sumber independen yang bisa diaudit,
limitation tetap. Jangan memakai OSRM sebagai ground truth.

## Celah pengukuran

Bukan fitur, tetapi harus ditutup sebelum klaim baru:

- [ ] Latency satu graph nasional penuh (bukan tiga region) pada 100 km.
- [ ] Bucket 80–100 km: `pairs-java.csv` berhenti di 79,9 km garis lurus.
- [ ] OSRM/Valhalla/GraphHopper pada graph nasional, jika ingin klaim eksternal.
- [ ] Dampak `restriction:conditional` dan ferry schedule terhadap ETA.
- [ ] Image prebuilt setelah perubahan binary.

## Aturan

- Setiap item yang mengubah rute wajib menjalankan golden test dan memperbarui
  `RESULTS.md`.
- Jangan menambah dependency eksternal (libosrm, tile server, DB) tanpa
  argumen budget.
- Item selesai dipindahkan ke `ROADMAP.md`/`RESULTS.md`, bukan ditandai `[x]`
  di sini.
