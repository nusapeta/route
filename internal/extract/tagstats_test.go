package extract

import (
	"strings"
	"testing"
)

func TestTagCensusAdd(t *testing.T) {
	c := NewTagCensus()
	c.Add(tags(
		"highway", "residential",
		"maxspeed", "30",
		"motorcycle", "yes",
		"surface", "asphalt",
		"oneway", "yes",
		"oneway:motorcycle", "no",
		"access:conditional", "no @ (Mo-Fr 07:00-09:00)",
		"motorcycle:conditional", "yes @ (Sa-Su)",
	))
	c.Add(tags("route", "ferry", "motor_vehicle", "no"))
	c.Add(tags("building", "yes", "surface", "wood"))
	c.Add(tags("highway", "path", "oneway:motorcycle", "yes @ (Mo-Fr)"))

	if c.Ways != 4 || c.HighwayWays != 2 || c.FerryWays != 1 {
		t.Fatalf("ways=%d highway=%d ferry=%d", c.Ways, c.HighwayWays, c.FerryWays)
	}
	if c.ByClass["residential"] != 1 || c.ByClass["path"] != 1 {
		t.Errorf("classes = %v", c.ByClass)
	}
	if c.Maxspeed != 1 || c.MaxspeedByClass["residential"] != 1 {
		t.Errorf("maxspeed = %d by class %v", c.Maxspeed, c.MaxspeedByClass)
	}
	if c.Motorcycle["yes"] != 1 || c.Surface["asphalt"] != 1 {
		t.Errorf("motorcycle=%v surface=%v", c.Motorcycle, c.Surface)
	}
	if _, ok := c.Surface["wood"]; ok {
		t.Errorf("non-highway surface was counted: %v", c.Surface)
	}
	if c.Oneway["yes"] != 1 || c.OnewayMotorcycle["no"] != 1 {
		t.Errorf("oneway=%v oneway:motorcycle=%v", c.Oneway, c.OnewayMotorcycle)
	}
	if c.OnewayMotorcycleSched != 1 {
		t.Errorf("scheduled oneway:motorcycle = %d", c.OnewayMotorcycleSched)
	}
	if c.Conditional["access:conditional"] != 1 || c.Conditional["motorcycle:conditional"] != 1 {
		t.Errorf("conditional = %v", c.Conditional)
	}
	if c.AccessCond != 1 || c.MotorcycleCond != 1 {
		t.Errorf("access cond=%d motorcycle cond=%d", c.AccessCond, c.MotorcycleCond)
	}

	first := c.String()
	if second := c.String(); first != second {
		t.Fatal("TagCensus.String is not deterministic")
	}
	for _, want := range []string{"highway_ways 2", `motorcycle "yes" 1`, "maxspeed_class \"residential\" 1 100.00%"} {
		if !strings.Contains(first, want) {
			t.Errorf("String missing %q:\n%s", want, first)
		}
	}
}
