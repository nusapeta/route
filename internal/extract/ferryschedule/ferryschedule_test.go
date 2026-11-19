package ferryschedule

import "testing"

func TestCrossingsTable(t *testing.T) {
	if len(Crossings) != 10 {
		t.Fatalf("crossings = %d, want 10", len(Crossings))
	}
	seen := make(map[string]bool, len(Crossings))
	for _, c := range Crossings {
		if seen[c.Name] {
			t.Errorf("duplicate name %q", c.Name)
		}
		seen[c.Name] = true
		if c.PortA == "" || c.PortB == "" || c.DurationS == 0 || c.Frequency == "" {
			t.Errorf("incomplete crossing %+v", c)
		}
	}
}

func TestLookup(t *testing.T) {
	for _, want := range Crossings {
		got, ok := Lookup(want.Name)
		if !ok || got != want {
			t.Errorf("Lookup(%q) = %+v, %v", want.Name, got, ok)
		}
	}
	c, ok := Lookup("Merak – Bakauheni")
	if !ok || c.Name != "Merak-Bakauheni" || c.DurationS != 5226 {
		t.Errorf("dash/spacing lookup = %+v, %v", c, ok)
	}
	for _, name := range []string{"", "Unknown-Strait", "Merak"} {
		if _, ok := Lookup(name); ok {
			t.Errorf("Lookup(%q) matched an unknown crossing", name)
		}
	}
}
