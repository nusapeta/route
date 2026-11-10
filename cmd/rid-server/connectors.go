package main

// ferryPort is one end of a cross-region ferry link. Coordinates point at the
// terminal road, not at the water: the leg routers snap these into their own
// graph before the crossing is added as a straight ferry segment.
type ferryPort struct {
	name string
	lat  float64
	lon  float64
}

// ferryConnector joins two region files across a strait. A route is only
// stitched when both endpoint regions match one side; every other cross-region
// pair stays a 422. Distances and times are the measured OSM values from the
// national graph (Merak-Bakauheni 28,535 m / 5,226 s, Ketapang-Gilimanuk
// 4,180 m / 1,436 s), so the static table does not invent a crossing.
type ferryConnector struct {
	name       string
	a, b       ferryPort
	regionA    []string
	regionB    []string
	distanceM  uint32
	durationDs uint32
}

// defaultFerryConnectors lists the only cross-file links the server serves.
// Keep this table small: it is an explicit coverage decision, not a fallback
// for arbitrary pairs.
func defaultFerryConnectors() []ferryConnector {
	return []ferryConnector{
		{
			name:       "Merak-Bakauheni",
			a:          ferryPort{"Merak", -5.9317, 105.9961},
			b:          ferryPort{"Bakauheni", -5.8706, 105.7546},
			regionA:    []string{"java"},
			regionB:    []string{"sumatra"},
			distanceM:  28535,
			durationDs: 52260,
		},
		{
			name:       "Ketapang-Gilimanuk",
			a:          ferryPort{"Ketapang", -8.1431, 114.3993},
			b:          ferryPort{"Gilimanuk", -8.1627, 114.4353},
			regionA:    []string{"java"},
			regionB:    []string{"nusa-tenggara", "bali", "bali-nusra", "nusra"},
			distanceM:  4180,
			durationDs: 14360,
		},
	}
}

// connectorFor returns the connector matching a region pair. The second port
// is swapped when the request runs in the opposite direction.
func connectorFor(connectors []ferryConnector, from, to string) (ferryConnector, ferryPort, ferryPort, bool) {
	for _, c := range connectors {
		if regionMatches(c.regionA, from) && regionMatches(c.regionB, to) {
			return c, c.a, c.b, true
		}
		if regionMatches(c.regionA, to) && regionMatches(c.regionB, from) {
			return c, c.b, c.a, true
		}
	}
	return ferryConnector{}, ferryPort{}, ferryPort{}, false
}

func regionMatches(names []string, got string) bool {
	for _, name := range names {
		if name == got {
			return true
		}
	}
	return false
}
