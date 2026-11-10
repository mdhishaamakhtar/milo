// Package maths holds Milo's numbers: unit display, volume, effective load,
// estimated one-rep max, calories and new bests. Every client computes these
// the same way; testdata/maths at the repo root pins the expected results.
package maths

import (
	"math"
	"strconv"
	"strings"
)

// KgPerLb is the exact international pound.
const KgPerLb = 0.45359237

// DefaultMET is used for an exercise with no MET of its own.
const DefaultMET = 5.0

// Measurements, as stored.
const (
	RepsLoad   = "reps_load"
	Bodyweight = "bodyweight"
	Assisted   = "assisted"
	Timed      = "timed"
	Distance   = "distance"
)

// ToKg converts an exact typed weight to kilograms. ok is false for an empty or unreadable weight.
func ToKg(weight, unit string) (kg float64, ok bool) {
	if weight == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(weight, 64)
	if err != nil {
		return 0, false
	}
	if unit == "lb" {
		return v * KgPerLb, true
	}
	return v, true
}

// DisplayWeight shows a stored weight in displayUnit, rounded to 0.1 with a
// trailing ".0" dropped: 35 lb in kg is "15.9", 20.41 kg in lb is "45".
// The stored weight is never changed by this.
func DisplayWeight(weight, unit, displayUnit string) string {
	kg, ok := ToKg(weight, unit)
	if !ok {
		return ""
	}
	v := kg
	if displayUnit == "lb" {
		v = kg / KgPerLb
	}
	return FormatTenths(v)
}

// FormatTenths rounds to one decimal place (half away from zero) and drops a trailing ".0".
func FormatTenths(v float64) string {
	s := strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// SetInput is one set as the maths needs it.
type SetInput struct {
	Measurement string `json:"measurement"`
	Weight      string `json:"weight"`
	Unit        string `json:"unit"`
	Reps        int    `json:"reps"`
	PerSide     bool   `json:"per_side"`
	Done        bool   `json:"done"`
}

// SetVolumeKg is weight × reps (× 2 for per-side sets) for a done reps-and-load set; 0 otherwise.
func SetVolumeKg(s SetInput) float64 {
	if !s.Done || s.Measurement != RepsLoad || s.Reps <= 0 {
		return 0
	}
	kg, ok := ToKg(s.Weight, s.Unit)
	if !ok {
		return 0
	}
	v := kg * float64(s.Reps)
	if s.PerSide {
		v *= 2
	}
	return v
}

// VolumeKg sums SetVolumeKg.
func VolumeKg(sets []SetInput) float64 {
	total := 0.0
	for _, s := range sets {
		total += SetVolumeKg(s)
	}
	return total
}

// EffectiveLoadKg is what was actually moved: the weight for reps-and-load sets,
// body weight plus any added weight for bodyweight sets, body weight minus the
// assistance (not below 0) for assisted sets. bodyKg 0 means unknown.
func EffectiveLoadKg(measurement, weight, unit string, bodyKg float64) (float64, bool) {
	kg, hasWeight := ToKg(weight, unit)
	switch measurement {
	case RepsLoad:
		return kg, hasWeight
	case Bodyweight:
		if bodyKg <= 0 {
			return 0, false
		}
		return bodyKg + kg, true
	case Assisted:
		if bodyKg <= 0 {
			return 0, false
		}
		return math.Max(0, bodyKg-kg), true
	}
	return 0, false
}

// E1RM is the Epley estimate of a one-rep max, for 1 to 12 reps (1 rep is the load itself).
func E1RM(loadKg float64, reps int) (float64, bool) {
	if loadKg <= 0 || reps < 1 || reps > 12 {
		return 0, false
	}
	if reps == 1 {
		return loadKg, true
	}
	return loadKg * (1 + float64(reps)/30), true
}

// Summary is one exercise's numbers for one session.
type Summary struct {
	VolumeKg  float64
	TopLoadKg float64 // heaviest effective load; ties go to more reps
	TopReps   int
	HasTop    bool
	E1RMKg    float64 // best estimate across the session's sets
	HasE1RM   bool
}

// Summarize computes a session's numbers for one exercise from its done sets.
func Summarize(sets []SetInput, bodyKg float64) Summary {
	sum := Summary{VolumeKg: VolumeKg(sets)}
	for _, s := range sets {
		if !s.Done || s.Reps <= 0 {
			continue
		}
		load, ok := EffectiveLoadKg(s.Measurement, s.Weight, s.Unit, bodyKg)
		if !ok {
			continue
		}
		if !sum.HasTop || load > sum.TopLoadKg || (load == sum.TopLoadKg && s.Reps > sum.TopReps) {
			sum.TopLoadKg, sum.TopReps, sum.HasTop = load, s.Reps, true
		}
		if e, ok := E1RM(load, s.Reps); ok && (!sum.HasE1RM || e > sum.E1RMKg) {
			sum.E1RMKg, sum.HasE1RM = e, true
		}
	}
	return sum
}

// ExerciseShare is one exercise's part of a workout, for calories.
type ExerciseShare struct {
	MET      *float64 `json:"met"`
	DoneSets int      `json:"done_sets"`
}

// ActiveKcal estimates active energy: for each exercise, (MET − 1) × body kg ×
// hours × its share of the workout's done sets. No estimate without body weight,
// duration or done sets.
func ActiveKcal(durationS, bodyKg float64, exercises []ExerciseShare) (float64, bool) {
	total := 0
	for _, e := range exercises {
		total += e.DoneSets
	}
	if bodyKg <= 0 || durationS <= 0 || total == 0 {
		return 0, false
	}
	hours := durationS / 3600
	kcal := 0.0
	for _, e := range exercises {
		met := DefaultMET
		if e.MET != nil {
			met = *e.MET
		}
		kcal += (met - 1) * bodyKg * hours * float64(e.DoneSets) / float64(total)
	}
	return kcal, true
}

// Session is one exercise's sets on one date, for finding bests.
type Session struct {
	Date string     `json:"date"`
	Sets []SetInput `json:"sets"`
}

// Best is a new best set in a session.
type Best struct {
	Date  string  `json:"date"`
	Kind  string  `json:"kind"` // "heaviest" or "e1rm"
	Value float64 `json:"value"`
	Reps  *int    `json:"reps"` // for "heaviest"
}

// NewBests walks sessions oldest first and reports each session that beat every
// earlier one on heaviest load or on estimated 1RM. The first session is the baseline.
func NewBests(sessions []Session, bodyKg float64) []Best {
	var out []Best
	var top, e1 float64
	var hasTop, hasE1 bool
	const eps = 1e-9
	for _, sess := range sessions {
		s := Summarize(sess.Sets, bodyKg)
		if s.HasTop {
			if hasTop && s.TopLoadKg > top+eps {
				reps := s.TopReps
				out = append(out, Best{Date: sess.Date, Kind: "heaviest", Value: s.TopLoadKg, Reps: &reps})
			}
			if !hasTop || s.TopLoadKg > top {
				top, hasTop = s.TopLoadKg, true
			}
		}
		if s.HasE1RM {
			if hasE1 && s.E1RMKg > e1+eps {
				out = append(out, Best{Date: sess.Date, Kind: "e1rm", Value: s.E1RMKg})
			}
			if !hasE1 || s.E1RMKg > e1 {
				e1, hasE1 = s.E1RMKg, true
			}
		}
	}
	return out
}
