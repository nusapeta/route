package extract

import (
	"testing"

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
		{"motorway car ok", ProfileCar, tags("highway", "motorway"), true, 100, false},
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

func TestSpeedClass(t *testing.T) {
	cases := map[float64]uint8{0: 1, 1: 2, 30: 60, 55: 110, 100: 200, 127: 254, 200: 254}
	for kmh, want := range cases {
		if got := speedClass(kmh); got != want {
			t.Errorf("speedClass(%v) = %d, want %d", kmh, got, want)
		}
	}
}

func TestParseProfile(t *testing.T) {
	for _, in := range []string{"", "car", "CAR", "motor", "Motor"} {
		if _, err := ParseProfile(in); err != nil {
			t.Errorf("ParseProfile(%q): %v", in, err)
		}
	}
	if _, err := ParseProfile("truck"); err == nil {
		t.Error("ParseProfile(truck) should fail")
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
		{"steps mc yes", tags("highway", "steps", "motorcycle", "yes"), true, 4, 0, false},
		{"track", tags("highway", "track"), true, 18, 0, false},
		{"residential", tags("highway", "residential"), true, 28, 0, false},
		{"service", tags("highway", "service"), true, 20, 0, false},
		{"living_street", tags("highway", "living_street"), true, 12, 0, false},
		{"motor_vehicle=no is not a bike ban", tags("highway", "primary", "motor_vehicle", "no"), true, 55, 0, false},
		{"access=no blocks", tags("highway", "primary", "access", "no"), false, 0, 0, false},
		{"access=private blocks", tags("highway", "primary", "access", "private"), false, 0, 0, false},
		{"access=destination allowed", tags("highway", "primary", "access", "destination"), true, 55, 0, false},
		{"motorcycle=no blocks", tags("highway", "primary", "motorcycle", "no"), false, 0, 0, false},
		{"motorcycle=destination currently allowed", tags("highway", "primary", "motorcycle", "destination"), true, 55, 0, false},
		{"oneway:motorcycle currently ignored", tags("highway", "primary", "oneway", "yes", "oneway:motorcycle", "no"), true, 55, 1, false},
		{"oneway=-1", tags("highway", "primary", "oneway", "-1"), true, 55, -1, false},
		{"roundabout", tags("highway", "primary", "junction", "roundabout"), true, 55, 1, false},
		{"unparsable maxspeed", tags("highway", "primary", "maxspeed", "0"), true, 55, 0, false},
		{"ferry ignores motor_vehicle=no", tags("route", "ferry", "motor_vehicle", "no"), true, 20, 0, true},
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
		if inf.speed != c.kmh {
			t.Errorf("%s: speed = %.1f, want %.1f", c.name, inf.speed, c.kmh)
		}
		if inf.dir != c.dir {
			t.Errorf("%s: dir = %d, want %d", c.name, inf.dir, c.dir)
		}
		if inf.ferry != c.ferry {
			t.Errorf("%s: ferry = %v, want %v", c.name, inf.ferry, c.ferry)
		}
	}
}
