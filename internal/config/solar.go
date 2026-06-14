package config

import (
	"math"
	"time"
)

// SunCalc resolves solar events for a date at a latitude/longitude. The returned
// instants are UTC; callers convert to local time via the schedule's location.
// Plain "at:" schedule entries never call this.
type SunCalc interface {
	Sunrise(date time.Time, lat, lon float64) time.Time
	Sunset(date time.Time, lat, lon float64) time.Time
}

// NOAASun implements SunCalc using the standard NOAA solar-position algorithm.
// It is pure (no I/O, no globals) and dependency-free.
type NOAASun struct{}

var _ SunCalc = NOAASun{}

const (
	deg2rad = math.Pi / 180
	rad2deg = 180 / math.Pi
	// Sun's geometric zenith at sunrise/sunset (90.833°: 0.833° refraction+radius).
	zenithDeg = 90.833
)

func (NOAASun) Sunrise(date time.Time, lat, lon float64) time.Time {
	return solarEvent(date, lat, lon, true)
}
func (NOAASun) Sunset(date time.Time, lat, lon float64) time.Time {
	return solarEvent(date, lat, lon, false)
}

// solarEvent computes the UTC instant of sunrise (rise=true) or sunset
// (rise=false) for the UTC calendar date of `date` at lat/lon.
// Implements the NOAA equations (Jean Meeus, Astronomical Algorithms).
func solarEvent(date time.Time, lat, lon float64, rise bool) time.Time {
	d := date.UTC()
	y, m, day := d.Date()

	// Julian day for 00:00 UTC of the calendar date.
	jd := julianDay(y, int(m), day)
	// Julian century from J2000.0, evaluated near solar noon (jd + 0.5).
	jc := (jd + 0.5 - 2451545.0) / 36525.0

	// Geometric mean longitude of the sun (deg).
	gml := math.Mod(280.46646+jc*(36000.76983+jc*0.0003032), 360)
	if gml < 0 {
		gml += 360
	}
	// Geometric mean anomaly (deg).
	gma := 357.52911 + jc*(35999.05029-0.0001537*jc)
	// Eccentricity of earth's orbit.
	ecc := 0.016708634 - jc*(0.000042037+0.0000001267*jc)
	// Sun's equation of center.
	gmaR := gma * deg2rad
	ctr := math.Sin(gmaR)*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(2*gmaR)*(0.019993-0.000101*jc) +
		math.Sin(3*gmaR)*0.000289
	trueLong := gml + ctr
	// Apparent longitude (deg).
	appLong := trueLong - 0.00569 - 0.00478*math.Sin((125.04-1934.136*jc)*deg2rad)
	// Mean obliquity of the ecliptic (deg) + correction.
	meanObliq := 23 + (26+(21.448-jc*(46.815+jc*(0.00059-jc*0.001813)))/60)/60
	obliq := meanObliq + 0.00256*math.Cos((125.04-1934.136*jc)*deg2rad)
	// Sun declination (deg).
	declR := math.Asin(math.Sin(obliq*deg2rad) * math.Sin(appLong*deg2rad))

	// Equation of time (minutes).
	vary := math.Tan(obliq/2*deg2rad) * math.Tan(obliq/2*deg2rad)
	gmlR := gml * deg2rad
	eqTime := 4 * rad2deg * (vary*math.Sin(2*gmlR) -
		2*ecc*math.Sin(gmaR) +
		4*ecc*vary*math.Sin(gmaR)*math.Cos(2*gmlR) -
		0.5*vary*vary*math.Sin(4*gmlR) -
		1.25*ecc*ecc*math.Sin(2*gmaR))

	// Hour angle (deg) for the configured zenith.
	latR := lat * deg2rad
	cosH := (math.Cos(zenithDeg*deg2rad) - math.Sin(latR)*math.Sin(declR)) /
		(math.Cos(latR) * math.Cos(declR))
	// Clamp for polar day/night so we still return a deterministic instant.
	if cosH > 1 {
		cosH = 1
	} else if cosH < -1 {
		cosH = -1
	}
	haDeg := math.Acos(cosH) * rad2deg

	// Solar noon (minutes after UTC midnight) at this longitude.
	noonMin := 720 - 4*lon - eqTime
	var eventMin float64
	if rise {
		eventMin = noonMin - 4*haDeg
	} else {
		eventMin = noonMin + 4*haDeg
	}

	// Build the UTC instant on the same calendar date, then add minutes.
	base := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(eventMin * float64(time.Minute)))
}

// julianDay returns the Julian Day Number for 00:00 UTC of the given Gregorian date.
func julianDay(y, m, d int) float64 {
	if m <= 2 {
		y--
		m += 12
	}
	a := y / 100
	b := 2 - a + a/4
	return math.Floor(365.25*float64(y+4716)) +
		math.Floor(30.6001*float64(m+1)) +
		float64(d) + float64(b) - 1524.5
}
