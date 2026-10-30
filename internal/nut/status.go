package nut

import (
	"strconv"
	"strings"
)

// The ups.status flags upsd reports (docs/new-drivers.txt of NUT).
const (
	FlagOnline      = "OL"      // on mains
	FlagOnBattery   = "OB"      // on battery
	FlagLowBattery  = "LB"      // low battery
	FlagHighBattery = "HB"      // high battery
	FlagReplace     = "RB"      // battery needs replacement
	FlagCharging    = "CHRG"    // battery charging
	FlagDischarging = "DISCHRG" // battery discharging
	FlagBypass      = "BYPASS"  // bypass active
	FlagCalibrating = "CAL"     // calibration
	FlagOff         = "OFF"     // output off
	FlagOverload    = "OVER"    // overload
	FlagTrim        = "TRIM"    // trimming the input voltage
	FlagBoost       = "BOOST"   // boosting the input voltage
	FlagFSD         = "FSD"     // forced shutdown ordered by the master
	FlagAlarm       = "ALARM"   // an alarm is set (ups.alarm)
)

// Status is a parsed ups.status.
type Status struct {
	Flags []string
}

// ParseStatus splits ups.status into its flags, upper-cased, in order, without duplicates.
func ParseStatus(s string) Status {
	var st Status
	seen := map[string]bool{}
	for _, f := range strings.Fields(s) {
		f = strings.ToUpper(f)
		if !seen[f] {
			seen[f] = true
			st.Flags = append(st.Flags, f)
		}
	}
	return st
}

// Has reports whether the flag is set.
func (s Status) Has(flag string) bool {
	for _, f := range s.Flags {
		if f == flag {
			return true
		}
	}
	return false
}

// OnBattery is OB (and not OL - a UPS in transition may say both; OB wins).
func (s Status) OnBattery() bool { return s.Has(FlagOnBattery) }

// Online is OL without OB.
func (s Status) Online() bool { return s.Has(FlagOnline) && !s.Has(FlagOnBattery) }

// String joins the flags again.
func (s Status) String() string { return strings.Join(s.Flags, " ") }

// Float reads a numeric variable; ok is false when it is missing or not a number.
func Float(vars map[string]string, name string) (float64, bool) {
	v, ok := vars[name]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
