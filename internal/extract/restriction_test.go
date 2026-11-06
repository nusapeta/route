package extract

import "testing"

func TestChainViaOrdering(t *testing.T) {
	// start 10, via ways given out of order and one reversed
	a := []int64{20, 21, 22}
	b := []int64{10, 20} // reversed relative to travel
	c := []int64{22, 30, 31}
	got, ok := chainVia([][]int64{a, c, b}, 10)
	if !ok {
		t.Fatal("chainVia failed")
	}
	want := []int64{10, 20, 21, 22, 30, 31}
	if len(got) != len(want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain = %v, want %v", got, want)
		}
	}
}

func TestChainViaGapFails(t *testing.T) {
	if _, ok := chainVia([][]int64{{99, 98}}, 10); ok {
		t.Fatal("chain with no shared endpoint must fail")
	}
}

func TestStatsString(t *testing.T) {
	s := RestrictionStats{Relations: 10, Usable: 9, ViaNode: 5, Ban: 7, Only: 2}
	got := s.String()
	if got == "" || s.Skipped() != 0 {
		t.Fatalf("unexpected stats %q skipped=%d", got, s.Skipped())
	}
}
