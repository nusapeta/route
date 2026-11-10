package main

import "testing"

func TestConnectorForDirection(t *testing.T) {
	cs := defaultFerryConnectors()
	c, a, b, ok := connectorFor(cs, "java", "sumatra")
	if !ok || c.name != "Merak-Bakauheni" || a.name != "Merak" || b.name != "Bakauheni" {
		t.Fatalf("java -> sumatra = %q, %q, %q, %v", c.name, a.name, b.name, ok)
	}
	c, a, b, ok = connectorFor(cs, "sumatra", "java")
	if !ok || c.name != "Merak-Bakauheni" || a.name != "Bakauheni" || b.name != "Merak" {
		t.Fatalf("sumatra -> java = %q, %q, %q, %v", c.name, a.name, b.name, ok)
	}
	if _, _, _, ok := connectorFor(cs, "java", "kalimantan"); ok {
		t.Fatal("unlisted pair got a connector")
	}
	if _, _, _, ok := connectorFor(cs, "java", "bali"); !ok {
		t.Fatal("bali alias should match Ketapang-Gilimanuk")
	}
}
