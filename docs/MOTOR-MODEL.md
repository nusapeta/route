# Model motor

Status: **berlaku 2026-11-10**. Dokumen ini mencatat apa yang dimodelkan
profil `motor`, apa yang sengaja tidak dimodelkan, dan bukti untuk setiap
batas. Sumber implementasi: `internal/extract/extract.go`. Rencana dan angka
lengkap: `docs/PLAN-2026-11-10.md` W1 dan W1b.

## 1. Yang dimodelkan

Profil dipilih saat build, bukan saat query. Graph mobil dan motor adalah dua
artifact terpisah karena snap region tidak boleh saling menutupi
(`deploy/docker-compose.yml`).

### Akses per kelas

- `motorcycle=yes|designated|permissive|official` membuka kelas yang tidak
  ada di tabel mobil: `path`, `footway`, `cycleway`, `pedestrian`, `steps`.
- `motorcycle=destination` adalah izin lemah. Way dengan
  `access=no`/`access=private` tetap bisa dimasuki sampai tujuan, termasuk
  kelas tambahan di atas, tetapi `motorcycle=no` tetap menolak.
- `motor_vehicle=no` tidak memblokir motor. `access=no` dan
  `access=private` memblokir kecuali ada izin motor di atas.
- `motorcycle=no` menolak, termasuk pada ferry.
- `highway=motorway` dibuang untuk motor kecuali ditandai `motorcycle=yes`.

Implementasi: `wayProfile`, `permitted`, `banned` di
`internal/extract/extract.go`.

### Arah

- `oneway:motorcycle=yes|true|1` menghasilkan arah maju.
- `oneway:motorcycle=-1|reverse` menghasilkan arah mundur.
- `oneway:motorcycle=no|false|0` menghasilkan dua arah.
- Nilai berjadwal, misalnya `yes @ (Mo-Fr)`, diabaikan dan jatuh kembali ke
  `oneway`. Graph statis tidak punya jam.

Implementasi: `direction(t wayTags, p Profile)`. Java hanya punya 288 way
dengan nilai tetap `oneway:motorcycle`: 254 `no`, 32 `yes`, dan 2 `-1`; 2 way
lain berjadwal (`docs/PLAN-2026-11-10.md` §1).

### Kecepatan kelas tambahan

Kelas `path`, `footway`, `cycleway`, `pedestrian`, dan `steps` memakai
`motorExtraKMH` sebagai kecepatan dasar. `surface` dan `smoothness` hanya
mengalikan kelas itu; jalan yang sudah ada di tabel mobil tidak berubah.

| `surface` | faktor |
|---|---:|
| `paved`, `asphalt`, `concrete` | 1,0 |
| `paving_stones`, `compacted`, `fine_gravel` | 0,8 |
| `gravel`, `dirt`, `ground`, `earth`, `unpaved` | 0,6 |
| `sand`, `mud` | 0,4 |

| `smoothness` | faktor |
|---|---:|
| `excellent` | 1,0 |
| `good` | 0,9 |
| `intermediate` | 0,75 |
| `bad` | 0,5 |
| `very_bad` | 0,4 |
| `horrible` | 0,3 |
| `very_horrible`, `impassable` | 0,25 |

Nilai akhir tidak turun di bawah 5 km/jam. Tag yang tidak dikenal bersifat
netral. Implementasi: `motorExtraSpeed`, `motorSurfaceFactor`,
`motorSmoothnessFactor`.

### Restriction dan ferry

- Restriction dibaca per kelas: `restriction:motorcycle` dan
  `restriction:motor_vehicle` untuk motor, `restriction:motorcar` dan
  `restriction:motor_vehicle` untuk mobil.
- `restriction:conditional` tetap di-skip. Build Java mencatat 167 relation
  conditional dari 4.654 relation yang dipindai.
- Ferry (`route=ferry`) menjadi edge dengan waktu dari tag `duration` bila
  ada. `motor_vehicle=no` diabaikan, `motorcycle=no` menolak.

## 2. Yang tidak dimodelkan

- Kemacetan, kepadatan lalu lintas, dan waktu tempuh per jam.
- Lane filtering dan perilaku menerobos arus.
- Ganjil-genap, jam sekolah, jam pasar, dan `*:conditional` lain.
- Parkir, waktu mencari parkir, dan perilaku pengendara.
- Cuaca, banjir, perbaikan jalan, dan penutupan sementara.
- Kecelakaan dan data probe kendaraan.
- Instruksi belok atau navigasi suara.

Graph hanya menyimpan topologi, kelas jalan, akses, dan waktu statis. Tidak
ada layer lalu lintas yang bisa dibaca saat query.

## 3. Kenapa tidak ada faktor kemacetan statis

Faktor seperti "motor 20% lebih cepat di arteri" tidak dimasukkan karena
tidak ada data yang bisa dikutip dan tidak bisa diverifikasi. Angka itu akan
mengubah klaim paper menjadi karangan, bukan model. Keputusan ini sudah
tertulis sebagai D2 di `docs/PLAN-2026-11-10.md`.

Yang boleh berubah tanpa data baru hanya aturan OSM yang eksplisit, seperti
`oneway:motorcycle`, `surface`, `smoothness`, dan `motorcycle=destination`.
Perubahan itu tetap harus lewat golden test, rebuild graph, dan A/B.

## 4. Batas dan cara membukanya

Batas model hanya boleh dibuka bila semua syarat berikut terpenuhi:

1. Ada sumber data yang bisa dikutip: trace GPS, data lalu lintas, atau
   ground truth independen.
2. Sumber punya tanggal, cakupan, dan izin pakai.
3. Ada metrik evaluasi tetap, bukan satu rute contoh.
4. Ada A/B sebelum/sesudah pada set pasangan yang sama.
5. Jika data bergantung waktu, format graph dan evaluasi `*:conditional`
   dibuka sebagai milestone baru. Format v4 tidak menyimpan jam.

Tag conditional yang sudah dihitung tetapi belum dievaluasi di Java:
`access:conditional` 940 way, `motorcycle:conditional` 47 way,
`oneway:motorcycle:conditional` 1 way. Angka ini berasal dari
`cmd/rid-tagstats` dan dicatat di `docs/PLAN-2026-11-10.md` W1.

## 5. Bukti terukur

Graph motor dibangun ulang 2026-11-10 dengan commit `920558c`.

| region | node | edge | byte | build |
|---|---:|---:|---:|---:|
| Java | 23.368.736 | 48.203.970 | 714.502.968 | 28 s |
| Sumatra | 11.934.134 | 24.466.989 | 365.707.149 | 11 s |
| Nusa Tenggara | 5.052.896 | 10.224.352 | 154.646.379 | 5 s |

Sejak format v5 satu file per region memuat mobil dan motor sekaligus:
Java 693.316.412 B, Sumatra 353.320.895 B, Nusa Tenggara 148.783.493 B
(total 1.195 MB), dibanding 2.445 MB untuk pasangan artefak v4. Angka di atas
tetap berlaku sebagai riwayat W1.

Ketiga artifact v4 memakai `profile_sha256` yang sama:
`1b7cabf164bfdf16d6df28e8c1d9b532365f8f4bf7923e511c938af40221a3a0`.
Graph lama memakai `a0f80ef144b819fa10a36f0dad477bba64935d73063a273fe611cf1c3b1b983d`.

Satu proses `-conc 4` memuat tiga graph:

- `/healthz` menyebut `java-motor`, `sumatra-motor`, dan
  `nusa-tenggara-motor`.
- Tujuh pasangan di `paper/results/pairs-motor-regions.csv` menjawab 200.
- Idle setelah `/healthz`: VmRSS 15.904 kB, RssAnon 7.236 kB.
- Setelah tujuh rute: VmRSS 83.380 kB, RssAnon 26.392 kB. Batas RssAnon
  beban adalah 700 MB; target rollout W1b 350 MB.

A/B 19 pasangan 0-5 km pada W1: 19/19 sukses, 0 perubahan geometri dan
durasi motor. Tidak ada pasangan di subset itu yang memakai tag baru; aturan
dibuktikan oleh `TestMotorGoldenRules` dan selisih graph, bukan oleh A/B itu.

## 6. Reproduksi

```bash
make graph REGION=java          # satu artefak, profil mobil + motor
make graph REGION=sumatra
make graph REGION=nusa-tenggara

go test ./internal/extract -run 'TestMotorGoldenRules|TestMotorOnlyRulesLeaveCarAlone'
go test ./internal/graph -run 'TestMotorGoldenRoutes'

bin/rid-server -profile motor \
  -graph data/java.graph \
  -graph data/sumatra.graph \
  -graph data/nusa-tenggara.graph \
  -addr 127.0.0.1:18081 -conc 4 -rate 0 -max-route-km 100
curl '127.0.0.1:18081/healthz'
```

## 7. Aturan perubahan

Setiap perubahan akses, arah, atau kecepatan motor wajib:

1. Menambah atau mengubah golden test.
2. Membangun ulang graph ketiga region.
3. Menjalankan A/B pada pasangan tetap.
4. Menaikkan versi aturan di `ProfileHash`.
5. Memperbarui dokumen ini bila batas model berubah.
