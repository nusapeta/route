// Package ferryschedule holds static reference data for the ferry crossings
// the router knows. OSM's duration tag still wins at extraction time; this
// table only fills gaps and annotates responses. No live timetable is queried.
package ferryschedule

import "strings"

// Crossing is one named ferry line. DurationS is a static crossing time in
// seconds; Frequency is a reference interval, not a departure timetable.
type Crossing struct {
	Name      string
	PortA     string
	PortB     string
	DurationS uint32
	Frequency string
}

// Crossings are the ten main crossings. The first two durations are measured
// from the national graph (paper/RESULTS.md 12c); the rest are static
// references with no official schedule in this repository.
var Crossings = [...]Crossing{
	{"Merak-Bakauheni", "Merak", "Bakauheni", 5226, "30 menit"},
	{"Ketapang-Gilimanuk", "Ketapang", "Gilimanuk", 1436, "20 menit"},
	{"Padang Bai-Lembar", "Padang Bai", "Lembar", 5400, "1 jam"},
	{"Lembar-Poto Tano", "Lembar", "Poto Tano", 7200, "3 kali sehari"},
	{"Poto Tano-Sape", "Poto Tano", "Sape", 5400, "2 kali sehari"},
	{"Sape-Bima", "Sape", "Bima", 3600, "harian"},
	{"Ujung-Kamal", "Ujung", "Kamal", 1800, "15 menit"},
	{"Batam-Tanjung Pinang", "Batam", "Tanjung Pinang", 7200, "30 menit"},
	{"Sibolga-Teluk Dalam", "Sibolga", "Teluk Dalam", 28800, "harian"},
	{"Makassar-Parepare", "Makassar", "Parepare", 25200, "harian"},
}

var dashSpace = strings.NewReplacer("–", "-", "—", "-", " ", "")

func normalize(s string) string {
	return strings.ToLower(dashSpace.Replace(s))
}

// Lookup matches an OSM name regardless of case, spacing or dash style, so
// "Merak – Bakauheni" resolves to the same row as "Merak-Bakauheni".
func Lookup(name string) (Crossing, bool) {
	key := normalize(name)
	if key == "" {
		return Crossing{}, false
	}
	for _, c := range Crossings {
		if normalize(c.Name) == key {
			return c, true
		}
	}
	return Crossing{}, false
}
