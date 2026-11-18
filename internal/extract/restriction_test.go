package extract

import (
	"strings"
	"testing"

	"github.com/paulmach/osm"

	"github.com/nusapeta/route/internal/graph"
)

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
	cases := []struct {
		name string
		s    RestrictionStats
		want string
	}{
		{
			"no skips",
			RestrictionStats{Relations: 10, Usable: 9, Missing: 1, ViaNode: 5, Ban: 7, Only: 2},
			"restrictions: seen=10 usable=9 via_node=5 via_way=0 ban=7 only=2 " +
				"skipped{not_car=0 not_class=0 missing=1 conditional=0 except=0 bad_type=0 " +
				"unresolved=0 chain=0 via_way_missing=0 relaxed=0 reach_cap=0 " +
				"why{no_node=0 not_on_way=0 no_in=0 no_out=0}}",
		},
		{
			"skip counters",
			RestrictionStats{Relations: 3, Missing: 2, ViaWayMissing: 1},
			"restrictions: seen=3 usable=0 via_node=0 via_way=0 ban=0 only=0 " +
				"skipped{not_car=0 not_class=0 missing=2 conditional=0 except=0 bad_type=0 " +
				"unresolved=0 chain=0 via_way_missing=1 relaxed=0 reach_cap=0 " +
				"why{no_node=0 not_on_way=0 no_in=0 no_out=0}}",
		},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("%s: String() = %q, want %q", c.name, got, c.want)
		}
		if got := c.s.Usable + c.s.Skipped(); got != c.s.Relations {
			t.Errorf("%s: usable+skipped = %d, want Relations %d", c.name, got, c.s.Relations)
		}
	}
}

func TestHasDetour(t *testing.T) {
	// 0 -> 1 -> 2, with 0 -> 1 the only way out of the source.
	v := &restView{
		off: []uint32{0, 1, 2, 3, 3},
		to:  []uint32{1, 2, 3},
	}
	banned := map[uint64]struct{}{graph.PackTurn(0, 1): {}}
	if ok, capped := v.hasDetour(0, 0, 1, banned, nil); ok || capped {
		t.Fatalf("detour through the banned transition: ok=%v capped=%v", ok, capped)
	}
	// 0 -> 4 -> 1 -> 2 leaves the source without taking 0 -> 1.
	v = &restView{
		off: []uint32{0, 2, 3, 4, 4, 5},
		to:  []uint32{1, 4, 2, 3, 1},
	}
	banned = map[uint64]struct{}{graph.PackTurn(0, 2): {}}
	if ok, capped := v.hasDetour(0, 0, 2, banned, nil); !ok || capped {
		t.Fatalf("detour not found: ok=%v capped=%v", ok, capped)
	}
}

func TestViaWayRelaxesWithoutDetour(t *testing.T) {
	v := &restView{
		wayIDs:   []int64{10, 11, 12},
		wayIndex: map[int64]int32{10: 0, 11: 1, 12: 2},
		flat:     []int64{0, 1, 1, 2, 2, 3},
		wayOff:   []int{0, 2, 4, 6},
		off:      []uint32{0, 1, 2, 3, 3},
		to:       []uint32{1, 2, 3},
		ids:      []int64{0, 1, 2, 3},
	}
	res := restResult{stats: RestrictionStats{Relations: 1, Usable: 1, ViaWay: 1}}
	if !res.addViaWay(v, 0, 2, []int64{11}, false) {
		t.Fatal("addViaWay failed")
	}
	res.filterDetours(v)
	if res.stats.Relaxed != 1 || res.stats.Ban != 0 || res.stats.ViaWay != 0 || len(res.ban) != 0 {
		t.Fatalf("stats=%+v ban=%v", res.stats, res.ban)
	}
}

func TestViaWayKeepsBanWithDetour(t *testing.T) {
	v := &restView{
		wayIDs:   []int64{10, 11, 12},
		wayIndex: map[int64]int32{10: 0, 11: 1, 12: 2},
		flat:     []int64{0, 1, 1, 2, 2, 3},
		wayOff:   []int{0, 2, 4, 6},
		off:      []uint32{0, 2, 3, 4, 4, 5},
		to:       []uint32{1, 4, 2, 3, 1},
		ids:      []int64{0, 1, 2, 3, 4},
	}
	res := restResult{stats: RestrictionStats{Relations: 1, Usable: 1, ViaWay: 1}}
	if !res.addViaWay(v, 0, 2, []int64{11}, false) {
		t.Fatal("addViaWay failed")
	}
	res.filterDetours(v)
	if res.stats.Ban != 1 || res.stats.ViaWay != 1 || len(res.ban) != 1 || res.ban[0] != graph.PackTurn(0, 2) {
		t.Fatalf("stats=%+v ban=%v", res.stats, res.ban)
	}
}

func TestRestrictionTagByProfile(t *testing.T) {
	cases := []struct {
		name string
		tags osm.Tags
		prof Profile
		want string
	}{
		{"plain car", osm.Tags{{Key: "restriction", Value: "no_left_turn"}}, ProfileCar, "no_left_turn"},
		{"motorcar overrides plain", osm.Tags{
			{Key: "restriction", Value: "no_left_turn"},
			{Key: "restriction:motorcar", Value: "no_u_turn"},
		}, ProfileCar, "no_u_turn"},
		{"motor_vehicle applies to car", osm.Tags{
			{Key: "restriction:motor_vehicle", Value: "no_right_turn"},
		}, ProfileCar, "no_right_turn"},
		{"motorcar ignored by motor", osm.Tags{
			{Key: "restriction:motorcar", Value: "no_u_turn"},
		}, ProfileMotor, ""},
		{"motorcycle for motor", osm.Tags{
			{Key: "restriction:motorcycle", Value: "no_straight_on"},
		}, ProfileMotor, "no_straight_on"},
		{"hgv only", osm.Tags{{Key: "restriction:hgv", Value: "no_left_turn"}}, ProfileCar, ""},
	}
	for _, c := range cases {
		if got := restrictionTag(c.tags, c.prof); got != c.want {
			t.Errorf("%s: restrictionTag = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExceptApplies(t *testing.T) {
	cases := []struct {
		tok  string
		prof Profile
		want bool
	}{
		{"motorcar", ProfileCar, true},
		{"motorcar", ProfileMotor, false},
		{"motorcycle", ProfileMotor, true},
		{"motorcycle", ProfileCar, false},
		{"motor_vehicle", ProfileCar, true},
		{"motor_vehicle", ProfileMotor, true},
		{"vehicle", ProfileMotor, true},
		{" psv ", ProfileCar, false},
	}
	for _, c := range cases {
		if got := exceptApplies(c.tok, c.prof); got != c.want {
			t.Errorf("exceptApplies(%q, %s) = %v, want %v", c.tok, c.prof, got, c.want)
		}
	}
}

func TestOtherClassTag(t *testing.T) {
	if !otherClassTag(osm.Tags{{Key: "restriction:hgv", Value: "no_left_turn"}}) {
		t.Error("hgv restriction must count as another class")
	}
	if otherClassTag(osm.Tags{{Key: "restriction", Value: "no_left_turn"}}) {
		t.Error("plain restriction is not another class")
	}
	if otherClassTag(osm.Tags{{Key: "restriction:conditional", Value: "no_left_turn @ (06:00-10:00)"}}) {
		t.Error("conditional is handled separately, not as a class")
	}
}

func TestConditionalRestrictionUnsupported(t *testing.T) {
	res := restResult{stats: RestrictionStats{Relations: 2}}
	for _, tags := range []osm.Tags{
		{
			{Key: "type", Value: "restriction"},
			{Key: "restriction:conditional", Value: "no_left_turn @ (Mo-Fr 07:00-09:00)"},
		},
		{
			{Key: "type", Value: "restriction"},
			{Key: "restriction", Value: "no_left_turn"},
			{Key: "restriction:conditional", Value: "no_left_turn @ (Mo-Fr 07:00-09:00)"},
		},
	} {
		res.addRelation(&restView{}, &osm.Relation{Tags: tags})
	}

	if res.stats.Conditional != 2 {
		t.Errorf("conditional = %d, want 2", res.stats.Conditional)
	}
	if res.stats.Usable != 0 || res.stats.Ban != 0 || res.stats.Only != 0 ||
		len(res.ban) != 0 || len(res.only) != 0 {
		t.Fatalf("conditional relation stored as a rule: stats=%+v ban=%v only=%v", res.stats, res.ban, res.only)
	}
	if got := res.stats.Skipped(); got != 2 {
		t.Errorf("skipped = %d, want 2", got)
	}
	if got := res.stats.ConditionalNote(); got != ConditionalUnsupportedReason {
		t.Errorf("ConditionalNote = %q, want %q", got, ConditionalUnsupportedReason)
	}
	if s := res.stats.String(); !strings.Contains(s, "conditional_note="+ConditionalUnsupportedReason) {
		t.Errorf("String() does not expose the reason: %s", s)
	}
}
