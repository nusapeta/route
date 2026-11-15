# Ringkasan

Perubahan apa ini?

## Jenis

- [ ] Perbaikan bug
- [ ] Fitur
- [ ] Dokumentasi
- [ ] Data atau graph
- [ ] CI atau build

## Bukti

Untuk perubahan perilaku rute, isi pasangan `from`/`to` dan keluaran
`nusa-graphinfo` (format, node, edge, hash PBF).

```text
from= to=
graphinfo:
```

Untuk klaim performa, sertakan mesin, commit, dan perintah reproduksi.

## Checklist

- [ ] `gofmt`, `go vet`, `go test -race`, `go build` lulus.
- [ ] Test regresi ada untuk bug perilaku.
- [ ] Dokumentasi kontrak diperbarui bila perlu.
- [ ] Tidak ada PBF, graph, atau file data besar.
- [ ] Commit memakai `Signed-off-by` (DCO).
