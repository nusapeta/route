package extract

import (
	"testing"

	"github.com/paulmach/osm"
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
	s := RestrictionStats{Relations: 10, Usable: 9, ViaNode: 5, Ban: 7, Only: 2}
	got := s.String()
	if got == "" || s.Skipped() != 0 {
		t.Fatalf("unexpected stats %q skipped=%d", got, s.Skipped())
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
