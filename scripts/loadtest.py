#!/usr/bin/env python3
"""Load test sederhana untuk /route (stdlib saja).

Contoh:
    scripts/loadtest.py http://127.0.0.1:8080 -from -7.84,110.39 -to -7.73,110.37 \
        -c 4 -n 40

Melaporkan RPS tercapai, latency klien p50/p95, latency server (header
X-Route-Ms) p50/p95, dan jumlah error.
"""
import argparse
import http.client
import statistics
import threading
import time
from urllib.parse import urlparse


def pct(v, q):
    if not v:
        return float("nan")
    v = sorted(v)
    i = min(len(v) - 1, int(q * (len(v) - 1) + 0.5))
    return v[i]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("base")
    ap.add_argument("-from", dest="f", required=True)
    ap.add_argument("-to", dest="t", required=True)
    ap.add_argument("-c", type=int, default=4, help="concurrency")
    ap.add_argument("-n", type=int, default=40, help="total request")
    ap.add_argument("--format", default="polyline")
    a = ap.parse_args()

    u = urlparse(a.base)
    path = f"/route?from={a.f}&to={a.t}&format={a.format}"
    per = [a.n // a.c] * a.c
    for i in range(a.n % a.c):
        per[i] += 1

    lat, srv, errs = [], [], []
    lock = threading.Lock()

    def worker(k):
        conn = http.client.HTTPConnection(u.hostname, u.port or 80, timeout=120)
        for _ in range(per[k]):
            t0 = time.perf_counter()
            try:
                conn.request("GET", path)
                r = conn.getresponse()
                body = r.read()
                dt = time.perf_counter() - t0
                ms = r.getheader("X-Route-Ms")
                with lock:
                    if r.status != 200:
                        errs.append(r.status)
                    else:
                        lat.append(dt * 1000)
                        if ms:
                            srv.append(float(ms))
                if r.status != 200:
                    body = b""
            except Exception as e:  # noqa: BLE001
                with lock:
                    errs.append(type(e).__name__)
                conn.close()
                conn = http.client.HTTPConnection(u.hostname, u.port or 80, timeout=120)
        conn.close()

    ts = [threading.Thread(target=worker, args=(i,)) for i in range(a.c)]
    t0 = time.perf_counter()
    for th in ts:
        th.start()
    for th in ts:
        th.join()
    wall = time.perf_counter() - t0

    print(f"concurrency={a.c} requests={a.n} wall={wall:.2f}s")
    print(f"RPS      : {a.n/wall:.2f}")
    print(f"klien    : p50={pct(lat,0.5):.0f}ms p95={pct(lat,0.95):.0f}ms max={max(lat) if lat else float('nan'):.0f}ms")
    print(f"server   : p50={pct(srv,0.5):.0f}ms p95={pct(srv,0.95):.0f}ms")
    print(f"error    : {len(errs)} {sorted(set(map(str,errs)))[:5]}")


if __name__ == "__main__":
    main()
