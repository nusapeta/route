package extract

import (
	"math"
	"testing"

	"github.com/nusapeta/route/internal/extract/ferryschedule"
	"github.com/paulmach/osm"
)

func tags(kv ...string) osm.Tags {
	out := make(osm.Tags, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, osm.Tag{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func TestWayProfileProfiles(t *testing.T) {
	cases := []struct {
		name  string
		p     Profile
		tags  osm.Tags
		ok    bool
		kmh   float64 // 0 = don't check
		ferry bool
	}{
		{"car residential", ProfileCar, tags("highway", "residential"), true, 25, false},
		{"motor residential", ProfileMotor, tags("highway", "residential"), true, 28, false},
		{"motorway car ok", ProfileCar, tags("highway", "motorway"), true, 90, false},
		{"motorway bike banned", ProfileMotor, tags("highway", "motorway"), false, 0, false},
		{"motorway bike allowed", ProfileMotor, tags("highway", "motorway", "motorcycle", "yes"), true, 90, false},
		{"motorcycle=no beats all", ProfileMotor, tags("highway", "primary", "motorcycle", "no"), false, 0, false},
		{"motor_vehicle=no car", ProfileCar, tags("highway", "primary", "motor_vehicle", "no"), false, 0, false},
		{"motor_vehicle=no bike keeps", ProfileMotor, tags("highway", "primary", "motor_vehicle", "no"), true, 55, false},
		{"access=private car", ProfileCar, tags("highway", "service", "access", "private"), false, 0, false},
		{"access=private bike no tag", ProfileMotor, tags("highway", "service", "access", "private"), false, 0, false},
		{"access=private bike yes", ProfileMotor, tags("highway", "service", "access", "private", "motorcycle", "yes"), true, 20, false},
		{"footway car skipped", ProfileCar, tags("highway", "footway"), false, 0, false},
		{"footway bike without tag", ProfileMotor, tags("highway", "footway"), false, 0, false},
		{"footway bike allowed", ProfileMotor, tags("highway", "footway", "motorcycle", "yes"), true, 10, false},
		{"maxspeed wins", ProfileCar, tags("highway", "trunk", "maxspeed", "60"), true, 60, false},
		{"ferry", ProfileCar, tags("route", "ferry", "motor_vehicle", "yes", "duration", "00:30"), true, 20, true},
		{"ferry unknown class", ProfileCar, tags("building", "yes"), false, 0, false},
	}
	for _, c := range cases {
		inf, ok := wayProfile(c.p, c.tags)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if c.kmh > 0 && inf.speed != c.kmh {
			t.Errorf("%s: speed = %.1f, want %.1f", c.name, inf.speed, c.kmh)
		}
		if inf.ferry != c.ferry {
			t.Errorf("%s: ferry = %v, want %v", c.name, inf.ferry, c.ferry)
		}
	}
}

func TestFerryDuration(t *testing.T) {
	cases := []struct {
		raw  string
		want float64
	}{
		{"00:30", 1800},
		{"2:00", 7200},
		{"01:15:30", 4530},
		{"45", 2700}, // plain minutes
		{"", 0},
		{"abc", 0},
		{"-5", 0},
		{"0", 0},
	}
	for _, c := range cases {
		got := ferryDuration(true, tags("duration", c.raw))
		if got != c.want {
			t.Errorf("duration %q = %v, want %v", c.raw, got, c.want)
		}
	}
	if got := ferryDuration(false, tags("duration", "00:30")); got != 0 {
		t.Errorf("non-ferry duration = %v, want 0", got)
	}
}

func TestFerryScheduleFallback(t *testing.T) {
	base := tags("route", "ferry", "motor_vehicle", "yes", "name", "Merak – Bakauheni")
	want, ok := ferryschedule.Lookup("Merak-Bakauheni")
	if !ok {
		t.Fatal("Merak-Bakauheni missing from schedule")
	}
	inf, ok := wayProfile(ProfileCar, base)
	if !ok || inf.durSec != float64(want.DurationS) {
		t.Errorf("schedule fallback = %v, %v; want %v", inf.durSec, ok, want.DurationS)
	}
	withOSM := tags("route", "ferry", "motor_vehicle", "yes", "name", "Merak-Bakauheni", "duration", "00:45")
	inf, ok = wayProfile(ProfileCar, withOSM)
	if !ok || inf.durSec != 2700 {
		t.Errorf("OSM duration must win: got %v, %v; want 2700", inf.durSec, ok)
	}
	unknown := tags("route", "ferry", "motor_vehicle", "yes", "name", "Unknown-Strait")
	inf, ok = wayProfile(ProfileCar, unknown)
	if !ok || inf.durSec != 0 || inf.speed != 20 {
		t.Errorf("unknown ferry = %+v, %v; want 20 km/h and no duration", inf, ok)
	}
}

func TestSpeedClass(t *testing.T) {
	cases := map[float64]uint8{0: 1, 1: 2, 30: 60, 55: 110, 100: 200, 127: 254, 200: 254}
	for kmh, want := range cases {
		if got := speedClass(kmh); got != want {
			t.Errorf("speedClass(%v) = %d, want %d", kmh, got, want)
		}
	}
}

func TestProfileHashPerProfile(t *testing.T) {
	car, motor := ProfileHash(ProfileCar), ProfileHash(ProfileMotor)
	if car == motor {
		t.Fatalf("car and motor profile hashes are equal: %s", car)
	}
	if again := ProfileHash(ProfileCar); again != car {
		t.Errorf("ProfileHash(ProfileCar) unstable: %s then %s", car, again)
	}
	if got := ProfileHash(ProfileMotor); got != motor {
		t.Errorf("ProfileHash(ProfileMotor) unstable: %s then %s", motor, got)
	}
}

func TestParseSpeedKMH(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"60", 60, true},
		{" 60 ", 60, true},
		{"12.5", 12.5, true},
		{"60 km/h", 60, true},
		{"30 mph", 30 * 1.609344, true},
		{"0", 0, false},
		{"", 0, false},
		{"abc", 0, false},
		{"50;30", 0, false},
		{"30 knots", 0, false},
		{"300", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSpeedKMH(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseSpeedKMH(%q) = %v,%v, want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// TestMotorGoldenRules pins the exact access, speed, and direction decisions
// for the tags that matter to a motorcycle. A profile change that alters one
// of these numbers must update this table deliberately.
func TestMotorGoldenRules(t *testing.T) {
	cases := []struct {
		name  string
		tags  osm.Tags
		ok    bool
		kmh   float64
		dir   int8
		ferry bool
	}{
		{"motorway dropped", tags("highway", "motorway"), false, 0, 0, false},
		{"motorway_link dropped", tags("highway", "motorway_link"), false, 0, 0, false},
		{"motorway mc yes", tags("highway", "motorway", "motorcycle", "yes"), true, 90, 0, false},
		{"motorway mc yes beats access=no", tags("highway", "motorway", "access", "no", "motorcycle", "yes"), true, 90, 0, false},
		{"motorway_link mc yes", tags("highway", "motorway_link", "motorcycle", "yes"), true, 50, 0, false},
		{"path mc yes", tags("highway", "path", "motorcycle", "yes"), true, 12, 0, false},
		{"footway mc yes", tags("highway", "footway", "motorcycle", "designated"), true, 10, 0, false},
		{"cycleway mc yes", tags("highway", "cycleway", "motorcycle", "permissive"), true, 15, 0, false},
		{"pedestrian mc yes", tags("highway", "pedestrian", "motorcycle", "official"), true, 8, 0, false},
		{"steps mc yes", tags("highway", "steps", "motorcycle", "yes"), true, 5, 0, false},
		{"path gravel", tags("highway", "path", "motorcycle", "yes", "surface", "gravel"), true, 7.2, 0, false},
		{"path smoothness bad", tags("highway", "path", "motorcycle", "yes", "smoothness", "bad"), true, 6, 0, false},
		{"path gravel + bad hits floor", tags("highway", "path", "motorcycle", "yes", "surface", "gravel", "smoothness", "bad"), true, 5, 0, false},
		{"path sand hits floor", tags("highway", "path", "motorcycle", "yes", "surface", "sand"), true, 5, 0, false},
		{"footway paving stones", tags("highway", "footway", "motorcycle", "yes", "surface", "paving_stones"), true, 8, 0, false},
		{"path asphalt neutral", tags("highway", "path", "motorcycle", "yes", "surface", "asphalt"), true, 12, 0, false},
		{"path destination weak", tags("highway", "path", "access", "no", "motorcycle", "destination"), true, 12, 0, false},
		{"track", tags("highway", "track"), true, 18, 0, false},
		{"residential", tags("highway", "residential"), true, 28, 0, false},
		{"service", tags("highway", "service"), true, 20, 0, false},
		{"living_street", tags("highway", "living_street"), true, 12, 0, false},
		{"residential ignores surface", tags("highway", "residential", "surface", "gravel"), true, 28, 0, false},
		{"track ignores smoothness", tags("highway", "track", "smoothness", "horrible"), true, 18, 0, false},
		{"motor_vehicle=no is not a bike ban", tags("highway", "primary", "motor_vehicle", "no"), true, 55, 0, false},
		{"access=no blocks", tags("highway", "primary", "access", "no"), false, 0, 0, false},
		{"access=private blocks", tags("highway", "primary", "access", "private"), false, 0, 0, false},
		{"access=destination allowed", tags("highway", "primary", "access", "destination"), true, 55, 0, false},
		{"motorcycle=no blocks", tags("highway", "primary", "motorcycle", "no"), false, 0, 0, false},
		{"motorcycle=destination weak permission", tags("highway", "primary", "access", "no", "motorcycle", "destination"), true, 55, 0, false},
		{"motorcycle=destination beats private", tags("highway", "primary", "access", "private", "motorcycle", "destination"), true, 55, 0, false},
		{"oneway:motorcycle=no overrides oneway", tags("highway", "primary", "oneway", "yes", "oneway:motorcycle", "no"), true, 55, 0, false},
		{"oneway:motorcycle=-1 overrides oneway", tags("highway", "primary", "oneway", "yes", "oneway:motorcycle", "-1"), true, 55, -1, false},
		{"oneway:motorcycle=yes overrides oneway=no", tags("highway", "primary", "oneway", "no", "oneway:motorcycle", "yes"), true, 55, 1, false},
		{"oneway:motorcycle schedule ignored", tags("highway", "primary", "oneway", "yes", "oneway:motorcycle", "yes @ (Mo-Fr)"), true, 55, 1, false},
		{"oneway:motorcycle=no on roundabout", tags("highway", "primary", "junction", "roundabout", "oneway:motorcycle", "no"), true, 55, 0, false},
		{"oneway=-1", tags("highway", "primary", "oneway", "-1"), true, 55, -1, false},
		{"roundabout", tags("highway", "primary", "junction", "roundabout"), true, 55, 1, false},
		{"unparsable maxspeed", tags("highway", "primary", "maxspeed", "0"), true, 55, 0, false},
		{"ferry ignores motor_vehicle=no", tags("route", "ferry", "motor_vehicle", "no"), true, 20, 0, true},
		{"ferry motorcycle=no blocks", tags("route", "ferry", "motorcycle", "no"), false, 0, 0, false},
		{"ferry access=no blocks", tags("route", "ferry", "access", "no"), false, 0, 0, false},
		{"ferry destination weak", tags("route", "ferry", "access", "no", "motorcycle", "destination"), true, 20, 0, true},
	}
	for _, c := range cases {
		inf, ok := wayProfile(ProfileMotor, c.tags)
		if ok != c.ok {
			t.Errorf("%s: ok = %v, want %v", c.name, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if math.Abs(inf.speed-c.kmh) > 1e-9 {
			t.Errorf("%s: speed = %.3f, want %.3f", c.name, inf.speed, c.kmh)
		}
		if inf.dir != c.dir {
			t.Errorf("%s: dir = %d, want %d", c.name, inf.dir, c.dir)
		}
		if inf.ferry != c.ferry {
			t.Errorf("%s: ferry = %v, want %v", c.name, inf.ferry, c.ferry)
		}
	}
}

func TestMotorOnlyRulesLeaveCarAlone(t *testing.T) {
	oneway := tags("highway", "primary", "oneway", "yes", "oneway:motorcycle", "no")
	car, ok := wayProfile(ProfileCar, oneway)
	if !ok || car.dir != 1 {
		t.Fatalf("car dir = %d, ok = %v; want 1, true", car.dir, ok)
	}

	rough := tags("highway", "path", "motorcycle", "yes", "surface", "gravel")
	if _, ok := wayProfile(ProfileCar, rough); ok {
		t.Fatal("car must still skip path, even with motorcycle=yes")
	}

	road := tags("highway", "residential", "surface", "gravel", "smoothness", "bad")
	carRoad, ok := wayProfile(ProfileCar, road)
	if !ok || carRoad.speed != 25 {
		t.Fatalf("car residential speed = %.1f, ok = %v; want 25, true", carRoad.speed, ok)
	}

	carSett, ok := wayProfile(ProfileCar, tags("highway", "residential", "surface", "sett"))
	if !ok || carSett.speed != 25 {
		t.Fatalf("car residential/sett speed = %.1f, ok = %v; want 25, true", carSett.speed, ok)
	}
}

// TestMotorSurfaceFactorTable locks every surface multiplier against a base
// where all four tiers stay above the 5 km/h floor, so a tier edit is visible.
func TestMotorSurfaceFactorTable(t *testing.T) {
	cases := []struct {
		surface string
		want    float64
	}{
		{"paved", 15}, {"asphalt", 15}, {"concrete", 15},
		{"chipseal", 15}, {"concrete:plates", 15}, {"concrete:slabs", 15},
		{"paving_stones", 12}, {"compacted", 12}, {"fine_gravel", 12},
		{"sett", 12}, {"cobblestone", 12}, {"bricks", 12}, {"grass_paver", 12},
		{"gravel", 9}, {"dirt", 9}, {"ground", 9}, {"earth", 9}, {"unpaved", 9},
		{"pebblestone", 9}, {"stone", 9}, {"rock", 9},
		{"sand", 6}, {"mud", 6}, {"grass", 6},
		{"unknown", 15},
	}
	for _, c := range cases {
		got := motorExtraSpeed(wayTags{highway: "cycleway", surface: c.surface})
		if got != c.want {
			t.Errorf("surface=%q: speed = %.3f, want %.3f", c.surface, got, c.want)
		}
	}
}

// TestMotorSmoothnessFactorTable locks the smoothness multipliers and the
// floor reached by the two lowest tiers on a 15 km/h base.
func TestMotorSmoothnessFactorTable(t *testing.T) {
	cases := []struct {
		smoothness string
		want       float64
	}{
		{"excellent", 15}, {"good", 13.5}, {"intermediate", 11.25},
		{"bad", 7.5}, {"very_bad", 6}, {"horrible", 4.5},
		{"very_horrible", 5}, {"impassable", 5},
		{"unknown", 15},
	}
	for _, c := range cases {
		got := motorExtraSpeed(wayTags{highway: "cycleway", smoothness: c.smoothness})
		if got != c.want {
			t.Errorf("smoothness=%q: speed = %.3f, want %.3f", c.smoothness, got, c.want)
		}
	}
}

// TestMotorQualityFactorsCompose checks the combined multiplier and the floor.
func TestMotorQualityFactorsCompose(t *testing.T) {
	got := motorExtraSpeed(wayTags{highway: "cycleway", surface: "paving_stones", smoothness: "good"})
	if want := 10.8; math.Abs(got-want) > 1e-9 {
		t.Errorf("surface+smoothness speed = %.3f, want %.3f", got, want)
	}
	got = motorExtraSpeed(wayTags{highway: "path", surface: "sand", smoothness: "very_horrible"})
	if got != motorMinSpeedKMH {
		t.Errorf("floor speed = %.3f, want %d", got, motorMinSpeedKMH)
	}
}
