package config

import (
	"testing"
	"time"
)

// withinMinutes asserts got is within tol minutes of want.
func withinMinutes(t *testing.T, label string, got, want time.Time, tol float64) {
	t.Helper()
	diff := got.Sub(want).Minutes()
	if diff < 0 {
		diff = -diff
	}
	if diff > tol {
		t.Errorf("%s: got %s, want ~%s (off by %.1f min, tol %.0f)",
			label, got.UTC().Format("15:04"), want.UTC().Format("15:04"), diff, tol)
	}
}

// New York City, 2026-06-21 (summer solstice). Published NOAA values (UTC):
// sunrise ~09:25 UTC (05:25 EDT), sunset ~00:31 UTC next day (20:31 EDT).
// We assert within 4 minutes — algorithmic vs. published rounding.
func TestSolarNYCSolstice(t *testing.T) {
	var sc SunCalc = NOAASun{}
	lat, lon := 40.7128, -74.0060
	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	wantRise := time.Date(2026, 6, 21, 9, 25, 0, 0, time.UTC)
	wantSet := time.Date(2026, 6, 22, 0, 31, 0, 0, time.UTC)

	withinMinutes(t, "NYC sunrise", sc.Sunrise(date, lat, lon), wantRise, 4)
	withinMinutes(t, "NYC sunset", sc.Sunset(date, lat, lon), wantSet, 4)
}

// London, 2026-03-20 (near equinox). Published (UTC): sunrise ~06:02, sunset ~18:14.
func TestSolarLondonEquinox(t *testing.T) {
	var sc SunCalc = NOAASun{}
	lat, lon := 51.5074, -0.1278
	date := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	wantRise := time.Date(2026, 3, 20, 6, 2, 0, 0, time.UTC)
	wantSet := time.Date(2026, 3, 20, 18, 14, 0, 0, time.UTC)

	withinMinutes(t, "London sunrise", sc.Sunrise(date, lat, lon), wantRise, 5)
	withinMinutes(t, "London sunset", sc.Sunset(date, lat, lon), wantSet, 5)
}

// Sunrise must be before sunset on a normal day, and both on the requested date.
func TestSolarOrderingAndDate(t *testing.T) {
	var sc SunCalc = NOAASun{}
	date := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	rise := sc.Sunrise(date, 40.7128, -74.0060)
	set := sc.Sunset(date, 40.7128, -74.0060)
	if !rise.Before(set) {
		t.Errorf("sunrise %s not before sunset %s", rise, set)
	}
	if rise.UTC().Year() != 2026 || rise.UTC().YearDay() != date.UTC().YearDay() {
		t.Errorf("sunrise %s not on requested UTC date", rise)
	}
}
