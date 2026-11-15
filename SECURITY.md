# Keamanan

## Melaporkan masalah

Jangan membuka issue publik untuk kerentanan yang bisa dieksploitasi. Gunakan
GitHub private security advisory di
<https://github.com/nusapeta/route/security/advisories/new>. Sertakan:

- versi commit (`/version`) atau image tag;
- langkah reproduksi;
- dampak yang mungkin;
- apakah masalah bisa dipicu lewat HTTP atau hanya lewat graph yang rusak.

Konfirmasi penerimaan dikirim dalam 7 hari, best-effort. Tidak ada SLA dan
tidak ada janji tanggal perbaikan. Perbaikan dicatat di `CHANGELOG.md` setelah
rilis.

## Model ancaman

| Aset | Ancaman | Mitigasi |
|---|---|---|
| Proses server | Input HTTP berbahaya | Validasi panjang, rentang, tipe; `-max-route-km`, `-max-expansions`, `-timeout` |
| Graph | File rusak atau dimodifikasi | Magic `RIDG`, versi format, batas offset saat mmap |
| Parser PBF | File berbahaya | `paulmach/osm`; graph dibangun offline di host |
| Dependency | Modul rentan | `govulncheck` di CI, Dependabot |
| Container | Akses tulis atau root | non-root UID 65532, graph read-only, tanpa shell di image |
| Data | Penyalahgunaan data OSM | atribusi ODbL di `/healthz` dan halaman demo |

## Batas

Nusapeta Route tidak menangani autentikasi, TLS, atau akun. Rate limit per IP
ada, tetapi bukan pengganti reverse proxy bila Anda butuh kontrol akses.
Jangan mengekspos `/metrics` ke internet; gateway memblokirnya dengan
`-block-metrics`.

## Yang dianggap kerentanan

- Crash atau panic dari request HTTP valid.
- Out-of-bounds read dari graph yang dibuka dengan verifikasi.
- Eksekusi kode dari input PBF atau graph.
- Kebocoran path filesystem atau stack trace di respons.
- Denial of service yang tidak bisa dibatasi oleh limit yang ada.
- Rate limit yang bisa dilewati lewat `X-Forwarded-For` saat `-trust-proxy`
  aktif tanpa proxy tepercaya di depannya.

## Yang bukan kerentanan

- Rute salah karena data OSM tidak lengkap atau tag akses yang keliru.
- Perbedaan hasil dengan OSRM atau navigasi komersial.
- Rute di atas 100 km yang dijawab 422; itu batas produk.
- Latency di hardware di bawah spesifikasi yang didokumentasikan.

## Dependency

- `go.mod` dan `go.sum` di-commit; CI menjalankan `go mod tidy -diff`.
- `govulncheck` dijalankan setiap push dan PR.
- Dependency baru harus dijelaskan di pull request.
- Graph dan PBF tidak pernah masuk git.
