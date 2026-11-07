package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// graphFingerprint identifies the loaded graphs plus the binary. A route answer
// depends only on those two things (the router is deterministic for a given
// epsilon), so the ETag can be built from it and the client can keep a route
// cached until the graph is rebuilt or the server is upgraded.
func graphFingerprint(regions []*region) string {
	h := sha256.New()
	for _, rg := range regions {
		prov := rg.g.Provenance()
		fmt.Fprintf(h, "%s|%d|%d|%s|%s|%s\n", rg.name, rg.g.NodeCount, rg.g.EdgeCount,
			prov.PBFHash, prov.BuiltAt, rg.g.MetaValue("extractor"))
	}
	io.WriteString(h, "commit="+commit)
	return short(h, 12)
}

// routeETag hashes the parsed request, not the raw query string, so
// "?from=1,2&to=3,4" and "?to=3,4&from=1,2" share a cache entry, and a POST body
// with the same points shares it with the GET form.
func routeETag(fp string, p routeParams) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%.6f,%.6f|%.6f,%.6f|%s|%.3f|%t|%t",
		fp, p.fLat, p.fLon, p.tLat, p.tLon, p.format, p.simplify, p.pin, p.restrictions)
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
