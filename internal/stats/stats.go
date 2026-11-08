// Package stats holds the summary helpers shared by the rid-* tools.
package stats

import "math"

// Percentile returns the q-quantile of an ascending slice, using the index
// round(q*(n-1)). An empty slice yields 0.
func Percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Round(q * float64(len(sorted)-1)))
	if i < 0 {
		i = 0
	} else if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}
