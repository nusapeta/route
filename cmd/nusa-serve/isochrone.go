package main

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"

	"github.com/nusapeta/route/internal/graph"
)

const (
	reasonIsoTooLarge = "isochrone_too_large"
	reasonIsoNoArea   = "no_area"
)

type isoParams struct {
	lat, lon        float64
	minutes, meters float64
	grid, simplify  float64
}

func readIsoParams(req *http.Request) (isoParams, error) {
	q := req.URL.Query()
	p := isoParams{
		minutes: 15, meters: graph.MaxIsoMeters,
		grid: graph.DefaultIsoGridM, simplify: graph.DefaultIsoSimplifyM,
	}
	num := func(key string, required bool, dst *float64) error {
		raw := q.Get(key)
		if raw == "" {
			if required {
				return errors.New(key + " is required")
			}
			return nil
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return errors.New(key + " must be a number")
		}
		*dst = v
		return nil
	}
	if err := num("lat", true, &p.lat); err != nil {
		return p, err
	}
	if err := num("lon", false, &p.lon); err != nil {
		return p, err
	}
	if p.lon == 0 {
		if err := num("lng", true, &p.lon); err != nil {
			return p, err
		}
	}
	if err := num("minutes", false, &p.minutes); err != nil {
		return p, err
	}
	if err := num("meters", false, &p.meters); err != nil {
		return p, err
	}
	if err := num("grid", false, &p.grid); err != nil {
		return p, err
	}
	if err := num("simplify", false, &p.simplify); err != nil {
		return p, err
	}
	switch {
	case p.minutes < 1 || p.minutes > 60:
		return p, errors.New("minutes must be between 1 and 60")
	case p.meters <= 0 || p.meters > graph.MaxIsoMeters:
		return p, errors.New("meters must be between 1 and " + strconv.Itoa(graph.MaxIsoMeters))
	case p.grid < 50 || p.grid > 10_000:
		return p, errors.New("grid must be between 50 and 10000 meters")
	case p.simplify < 0:
		return p, errors.New("simplify must be non-negative")
	}
	return p, nil
}

func (s *server) handleIsochrone(w http.ResponseWriter, req *http.Request) {
	p, err := readIsoParams(req)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	if !inIndonesia(p.lat, p.lon) {
		httpErr(w, 400, "coordinates outside Indonesia")
		return
	}
	if !s.rl.allowRequest(w, req, s.m) {
		return
	}
	rg, _, ok := s.pickSnap(p.lat, p.lon, 0)
	if !ok {
		s.m.errSnap.Add(1)
		httpErrReason(w, 422, reasonSnap, "no road within snap distance (region not loaded?)")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), s.timeout)
	defer cancel()
	res, err := graph.Isochrone(ctx, rg.r, p.lat, p.lon, graph.IsoOptions{
		Minutes: p.minutes, Meters: p.meters,
		GridM: p.grid, SimplifyM: p.simplify,
	})
	if err != nil {
		switch {
		case errors.Is(err, graph.ErrIsoTooLarge):
			httpErrReason(w, 422, reasonIsoTooLarge, err.Error())
		case errors.Is(err, graph.ErrIsoNoArea):
			httpErrReason(w, 422, reasonIsoNoArea, err.Error())
		case errors.Is(err, graph.ErrIsoNoSnap):
			httpErrReason(w, 422, reasonSnap, err.Error())
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
			httpErrReason(w, 504, reasonDeadline, "deadline exceeded ("+s.timeout.String()+")")
		default:
			log.Printf("isochrone error: %v", err)
			httpErr(w, 500, "internal error")
		}
		return
	}

	coords := make([][][2]float64, 0, len(res.RingLengths))
	off := 0
	for _, n := range res.RingLengths {
		ring := make([][2]float64, 0, n)
		for _, pt := range res.Rings[off : off+n] {
			ring = append(ring, [2]float64{pt[1], pt[0]}) // GeoJSON is lng,lat
		}
		coords = append(coords, ring)
		off += n
	}
	type geometry struct {
		Type        string         `json:"type"`
		Coordinates [][][2]float64 `json:"coordinates"`
	}
	type feature struct {
		Type     string   `json:"type"`
		Geometry geometry `json:"geometry"`
	}
	body := struct {
		Type     string         `json:"type"`
		Features []feature      `json:"features"`
		Meta     map[string]any `json:"meta"`
	}{
		Type: "FeatureCollection",
		Meta: map[string]any{
			"grid_m": res.GridM, "nodes_visited": res.NodesVisited,
			"area_m2": res.AreaM2, "minutes": p.minutes,
		},
	}
	for _, ring := range coords {
		body.Features = append(body.Features, feature{
			Type:     "Feature",
			Geometry: geometry{Type: "Polygon", Coordinates: [][][2]float64{ring}},
		})
	}
	writeJSON(w, body)
}
