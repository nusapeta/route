# Kontribusi

Nusapeta Route adalah router OSM untuk Indonesia: satu graph bisa dimuat dengan
mmap, satu binary, tanpa database. Kontribusi kecil tetap berarti, terutama
laporan rute salah dan perbaikan data.

## Sebelum mengubah

1. Baca [README.md](README.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md),
   dan dokumen yang terkait perubahan.
2. Cek [docs/BACKLOG.md](docs/BACKLOG.md) untuk pekerjaan yang sudah diketahui.
3. Perubahan kontrak HTTP atau format `.graph` butuh diskusi issue dulu.
4. Jangan mengubah `paper/RESULTS.md` atau `paper/COMPARISON.md`; itu snapshot
   pengukuran. Perbaikan dokumentasi ada di berkas lain.

## Setup

Butuh Go 1.27+ dan `make`.

```bash
git clone https://github.com/nusapeta/route.git
cd route
make build
make java                  # unduh PBF Jawa + build data/java.graph
bin/nusa-serve -graph data/java.graph -addr 127.0.0.1:8080
```

Perintah yang dipakai CI:

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test -race ./...
go build ./...
go mod tidy -diff
```

Jangan menambahkan dependency tanpa alasan. Graph dan PBF tidak masuk git.

## Aturan angka

Setiap PR yang mengubah perilaku rute wajib menyertakan:

- pasangan `from`/`to` yang diuji;
- keluaran `bin/nusa-graphinfo -graph ...` (versi format, jumlah node/edge,
  hash PBF) supaya penguji tahu graph mana yang dipakai;
- untuk klaim performa: mesin, commit, dan perintah reproduksi.

Angka tanpa mesin, commit, dan perintah ditolak. Klaim akurasi mengikuti
[paper/COMPARISON.md](paper/COMPARISON.md): jangan mengutip gate E1 sebagai
lulus.

## Kode

- `gofmt` wajib; jangan reformat berkas yang tidak disentuh.
- Setiap bug perilaku butuh test regresi.
- `unsafe` hanya untuk mmap dan pembacaan record fixed-size. Setiap blok
  `unsafe` wajib punya komentar alasan.
- Jangan menambah abstraksi baru untuk satu pemakai.
- Jangan menaruh domain pribadi, IP LAN, atau token di repo. Gunakan contoh
  `route.example.com` dan `127.0.0.1`.

## Pull request

- [ ] `gofmt`, `go vet`, `go test -race`, dan `go build` lulus.
- [ ] Perubahan perilaku rute menyertakan pasangan uji + `nusa-graphinfo`.
- [ ] Klaim performa menyertakan mesin, commit, dan perintah.
- [ ] Dokumentasi API atau format diperbarui bila kontrak berubah.
- [ ] Tidak ada PBF, graph, index, atau file data besar.

Commit memakai pesan imperatif dan menyebut area, misalnya
`serve: reject coordinates outside graph bounds`.

## DCO

Repositori memakai Developer Certificate of Origin 1.1, tanpa CLA. Setiap
commit menyertakan baris:

```text
Signed-off-by: Nama Anda <email@contoh.com>
```

Tambahkan dengan `git commit -s`. Kontribusi tanpa sign-off tidak digabung.

## Lisensi

Dengan mengirim kontribusi, Anda setuju kontribusi dilisensikan MIT. Data OSM
dan turunannya tetap ODbL 1.0; lihat [LICENSE-DATA](LICENSE-DATA).
