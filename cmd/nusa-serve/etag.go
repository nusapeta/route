package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// graphFingerprint identifies the loaded graphs, the binary, and the settings
// that change an answer: the route distance cap, the snap radius, the heuristic
// weight, and the turn penalty. The router is deterministic per (eps,
// turnScale), and the snap radius decides whether a point routes at all, so a
// change to any of them must invalidate cached routes. The workspace budget is
// deliberately left out: it only sizes the search scratch space, so it can turn
// a request into an error but never into a different answer.
func graphFingerprint(regions []*region, maxRouteM, maxSnapM float64, eps, turnScale float32) string {
	h := sha256.New()
	for _, rg := range regions {
		prov := rg.g.Provenance()
		fmt.Fprintf(h, "%s|%d|%d|%s|%s|%s\n", rg.name, rg.g.NodeCount, rg.g.EdgeCount,
			prov.PBFHash, prov.BuiltAt, rg.g.MetaValue("extractor"))
	}
	fmt.Fprintf(h, "max_route_m=%.0f\nsnap_m=%.0f\n", maxRouteM, maxSnapM)
	fmt.Fprintf(h, "eps=%.4f\nturn_scale=%.4f\n", eps, turnScale)
	io.WriteString(h, "commit="+commit)
	return short(h, 12)
}

// routeETag hashes the parsed request, not the raw query string, so
// "?from=1,2&to=3,4" and "?to=3,4&from=1,2" share a cache entry, and a POST body
// with the same points shares it with the GET form.
func routeETag(fp string, p routeParams) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%.6f,%.6f|%.6f,%.6f|%s|%.3f|%t|%t|%d|%d",
		fp, p.fLat, p.fLon, p.tLat, p.tLon, p.format, p.simplify, p.pin, p.restrictions, uint8(p.avoid), p.alt)
	for _, pt := range p.via {
		fmt.Fprintf(h, "|%.6f,%.6f", pt[0], pt[1])
	}
	return `"` + fp[:8] + "-" + short(h, 16) + `"`
}

func short(h hash.Hash, n int) string {
	return hex.EncodeToString(h.Sum(nil))[:n]
}

// etagMatches implements the If-None-Match comparison: a list of tags, "*", or
// the weak form W/"..." all match our strong tag.
func etagMatches(header, etag string) bool {
	if header == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == etag {
			return true
		}
	}
	return false
}
