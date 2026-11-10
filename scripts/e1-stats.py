#!/usr/bin/env python3
"""Summarise an E1 CSV produced by m6-compare.py.

Filter is the same as the paper: snap at most 250 m, snap gap at most 50 m.
Distance p95 is the acceptance metric before D4; duration p95 is reported
because D4 switches the primary metric when distance misses its gate.
"""
import argparse
import csv
import statistics


def num(row, key):
    try:
        return float(row[key])
    except (KeyError, TypeError, ValueError):
        return None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("csv")
    args = ap.parse_args()

    rows = list(csv.DictReader(open(args.csv, newline="")))
    matched, failed, excluded = [], 0, 0
    for row in rows:
        if row.get("ride_err") or row.get("osrm_err"):
            failed += 1
            continue
        snap = [num(row, k) for k in ("ride_snap_f", "ride_snap_t",
                                       "osrm_snap_f", "osrm_snap_t")]
        gap = [num(row, k) for k in ("snap_gap_f", "snap_gap_t")]
        if any(v is None or v > 250 for v in snap) or any(v is None or v > 50 for v in gap):
            excluded += 1
            continue
        matched.append(row)

    dist = sorted(abs(num(r, "dist_delta_pct")) for r in matched)
    dur = sorted((num(r, "dur_ratio") - 1.0) * 100.0 for r in matched)

    def pct(vals, p):
        return vals[int(p * (len(vals) - 1) + 0.5)]

    print(f"matches={len(matched)} fails={failed} excluded={excluded}")
    print(f"p50={pct(dist, .50):.2f}% p95={pct(dist, .95):.2f}% max={max(dist):.2f}%")
    print(f"gt10={sum(v > 10 for v in dist)} gt25={sum(v > 25 for v in dist)}")
    print(f"dur_p50={pct(dur, .50):+.2f}% dur_p95={pct(dur, .95):+.2f}% "
          f"dur_abs_p95={pct(sorted(abs(v) for v in dur), .95):.2f}%")


if __name__ == "__main__":
    main()
