package graph

import "testing"

// TestForwardSize checks the bounded reachability count that recognises a start
// stuck in a pocket.
func TestForwardSize(t *testing.T) {
	g := spurGraph(t)

	cases := []struct {
		node  uint32
		limit int
		want  int
	}{
		{3, 8, 1}, // spur end: no out-edges
		{4, 8, 2}, // 4 -> 3
		{0, 8, 4}, // 0, 1, 2, 5
		{0, 2, 3}, // stops once the count passes the limit
		{0, 4, 4}, // exactly at the limit
	}
	for _, c := range cases {
		if got := g.ForwardSize(c.node, c.limit); got != c.want {
			t.Errorf("ForwardSize(%d, %d) = %d, want %d", c.node, c.limit, got, c.want)
		}
	}
}
