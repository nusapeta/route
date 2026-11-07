#!/usr/bin/env bash
# M6 / E1: bangun dataset OSRM (MLD) dari pbf yang sama dengan routeid, catat
# waktu + peak RSS + ukuran artefak, lalu jalankan server untuk dibandingkan.
#
#   scripts/m6-osrm.sh extract data/java-latest.osm.pbf     # extract+partition+customize
#   scripts/m6-osrm.sh serve   data/java-latest.osrm        # osrm-routed MLD di :5000
#   scripts/m6-osrm.sh sizes   data/java-latest.osrm        # ukuran artefak
#
# Kenapa MLD bukan CH: CH butuh waktu customize jauh lebih besar tanpa
# keuntungan untuk perbandingan "ringan"; MLD adalah konfigurasi yang dipakai
# untuk deployment hemat memori.
#
# RAM: osrm-extract untuk Jawa butuh ~4-6 GB. Mesin 8 GB wajib punya swap, dan
# jangan jalankan bersamaan dengan rebuild graph routeid atau load test: angka
# waktu/RSS jadi sampah.
set -euo pipefail

IMG=${OSRM_IMG:-osrm/osrm-backend:latest}
MODE=${1:-}
ARG=${2:-}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
DATA=$ROOT/data

# peak RSS tahap docker: container diberi nama, RSS disampel dari docker stats
# (proses di dalam container tidak terlihat oleh `ps` di host).
run_stage() { # run_stage <nama> <args...>
	local name=$1
	shift
	local t0 peak=0 cur
	t0=$(date +%s)
	docker rm -f "$name" >/dev/null 2>&1 || true
	docker run -d --name "$name" -v "$DATA:/data" "$IMG" "$@" >/dev/null
	while [ "$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null)" = "true" ]; do
		# docker stats melaporkan "123.4MiB" / "1.234GiB"; satuannya wajib
		# dikalikan, jangan cuma dibuang hurufnya (bug lama: 5GiB -> 5MB).
		cur=$(docker stats --no-stream --format '{{.MemUsage}}' "$name" 2>/dev/null |
			awk '{v=$1; n=v+0; u=substr(v,length(v)-2);
			      if (u=="GiB") n*=1024; else if (u=="KiB") n/=1024;
			      else if (u=="TiB") n*=1024*1024;
			      printf "%d", n}')
		[ -n "${cur:-}" ] && [ "$cur" -gt "$peak" ] && peak=$cur
		sleep 2
	done
	# --rm ditidak dipakai: exit code harus dibaca sebelum container hilang
	local code
	code=$(docker inspect -f '{{.State.ExitCode}}' "$name" 2>/dev/null || echo "?")
	docker rm -f "$name" >/dev/null 2>&1 || true
	local peak_txt="~${peak}MB"
	[ "$peak" -eq 0 ] && peak_txt="n/a (stage <2s)"
	printf '%-16s wall=%ss peak_rss=%s exit=%s\n' "$name" "$(( $(date +%s) - t0 ))" "$peak_txt" "$code"
}

case "$MODE" in
extract)
	[ -n "$ARG" ] || { echo "usage: $0 extract <file.osm.pbf>" >&2; exit 2; }
	base=/data/$(basename "$ARG" .osm.pbf)
	docker pull "$IMG" >/dev/null
	run_stage osrm-extract osrm-extract -p /opt/car.lua "${base}.osm.pbf"
	run_stage osrm-partition osrm-partition "${base}.osrm"
	run_stage osrm-customize osrm-customize "${base}.osrm"
	echo "== artefak"
	du -ch "${DATA}/$(basename "$base")".osrm* | tail -1
	echo "== routeid untuk banding: $(du -sh "${DATA}/$(basename "$base" | sed 's/-latest//').graph" 2>/dev/null | cut -f1) (kalau ada)"
	;;
serve)
	[ -n "$ARG" ] || { echo "usage: $0 serve <file.osrm>" >&2; exit 2; }
	exec docker run --rm --name osrm-routed -p 5000:5000 -v "$DATA:/data" "$IMG" \
		osrm-routed --algorithm mld "$(basename "/data/$ARG")"
	;;
sizes)
	[ -n "$ARG" ] || { echo "usage: $0 sizes <file.osrm>" >&2; exit 2; }
	du -ch "${DATA}/$(basename "${ARG%.osrm}")".osrm* | tail -1
	;;
*)
	sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
	exit 2
	;;
esac
