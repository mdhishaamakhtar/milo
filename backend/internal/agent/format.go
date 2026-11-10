package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
)

// How Milo writes numbers, for the model and for cards. Weights stay in the unit they were typed in.

func weightText(weight, unit string) string {
	if weight == "" {
		return ""
	}
	return trimNum(weight) + " " + unit
}

func trimNum(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// setText is one set the way a person says it: "20 kg × 15", "45 s", "15 reps", "20 kg × 12 each side".
func setText(measurement, weight, unit string, reps *int32, perSide bool, durationS *int32, distanceM *float64) string {
	var parts []string
	switch measurement {
	case maths.Timed:
		if durationS != nil {
			parts = append(parts, durationText(int(*durationS)))
		}
		if weight != "" {
			parts = append(parts, "at "+weightText(weight, unit))
		}
	case maths.Distance:
		if distanceM != nil {
			parts = append(parts, distanceText(*distanceM))
		}
		if durationS != nil {
			parts = append(parts, "in "+durationText(int(*durationS)))
		}
		if weight != "" {
			parts = append(parts, "with "+weightText(weight, unit))
		}
	case maths.Bodyweight:
		r := 0
		if reps != nil {
			r = int(*reps)
		}
		if weight != "" {
			parts = append(parts, fmt.Sprintf("+%s × %d", weightText(weight, unit), r))
		} else {
			parts = append(parts, fmt.Sprintf("%d reps", r))
		}
	default:
		r := 0
		if reps != nil {
			r = int(*reps)
		}
		if weight != "" {
			parts = append(parts, fmt.Sprintf("%s × %d", weightText(weight, unit), r))
		} else {
			parts = append(parts, fmt.Sprintf("%d reps", r))
		}
	}
	text := strings.Join(parts, " ")
	if perSide {
		text += " each side"
	}
	return text
}

func durationText(s int) string {
	if s < 60 {
		return fmt.Sprintf("%d s", s)
	}
	if s%60 == 0 {
		return fmt.Sprintf("%d min", s/60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func distanceText(m float64) string {
	if m >= 1000 {
		return trimNum(strconv.FormatFloat(m/1000, 'f', 2, 64)) + " km"
	}
	return trimNum(strconv.FormatFloat(m, 'f', 0, 64)) + " m"
}

func setTextRow(measurement string, s query.Set) string {
	return kindPrefix(s.Kind) + setText(measurement, s.Weight, s.WeightUnit, s.Reps, s.PerSide, s.DurationS, s.DistanceM)
}

// exerciseSummary is the short line for an exercise in a workout: "6 × 15 at 20 kg", "3 × 45 s", "7 sets, top 22.7 kg".
func exerciseSummary(measurement string, sets []query.Set) string {
	var done []query.Set
	for _, s := range sets {
		if s.Done {
			done = append(done, s)
		}
	}
	if len(done) == 0 {
		return "no sets done"
	}
	first := setTextRow(measurement, done[0])
	same := true
	for _, s := range done[1:] {
		if setTextRow(measurement, s) != first {
			same = false
			break
		}
	}
	if same {
		if measurement == maths.Timed || done[0].Weight == "" {
			return fmt.Sprintf("%d × %s", len(done), first)
		}
		r := int32(0)
		if done[0].Reps != nil {
			r = *done[0].Reps
		}
		text := fmt.Sprintf("%d × %d at %s", len(done), r, weightText(done[0].Weight, done[0].WeightUnit))
		if done[0].PerSide {
			text += " each side"
		}
		return text
	}
	top := done[0]
	topKg, _ := maths.ToKg(top.Weight, top.WeightUnit)
	for _, s := range done[1:] {
		if kg, ok := maths.ToKg(s.Weight, s.WeightUnit); ok && kg > topKg {
			top, topKg = s, kg
		}
	}
	if top.Weight == "" {
		return fmt.Sprintf("%d sets", len(done))
	}
	return fmt.Sprintf("%d sets, top %s", len(done), weightText(top.Weight, top.WeightUnit))
}

func setInputs(measurement string, sets []query.Set) []maths.SetInput {
	out := make([]maths.SetInput, 0, len(sets))
	for _, s := range sets {
		r := 0
		if s.Reps != nil {
			r = int(*s.Reps)
		}
		out = append(out, maths.SetInput{Measurement: measurement, Weight: s.Weight, Unit: s.WeightUnit, Reps: r, PerSide: s.PerSide, Done: s.Done, Kind: s.Kind})
	}
	return out
}

// displayKg is a kilogram value in the user's display unit.
func displayKg(kg float64, unit string) float64 {
	if unit == "lb" {
		return kg / maths.KgPerLb
	}
	return kg
}

// kgText shows a kilogram total in the user's display unit with thousands separators: "2,142 kg".
func kgText(kg float64, displayUnit string) string {
	v := kg
	if displayUnit == "lb" {
		v = kg / maths.KgPerLb
	}
	return thousands(int64(v+0.5)) + " " + displayUnit
}

func thousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	if n < 0 {
		return "-" + thousands(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// dayText is "Tue 6 Oct", with the year when it isn't the current one.
func dayText(t time.Time, now time.Time) string {
	if t.Year() != now.Year() {
		return t.Format("Mon 2 Jan 2006")
	}
	return t.Format("Mon 2 Jan")
}

// dateText is "6 Oct", with the year when it isn't the current one: a day for a title.
func dateText(t time.Time, now time.Time) string {
	if t.Year() != now.Year() {
		return t.Format("2 Jan 2006")
	}
	return t.Format("2 Jan")
}

func minutesBetween(a time.Time, b *time.Time) int {
	if b == nil || b.Before(a) {
		return 0
	}
	return int(b.Sub(a).Round(time.Minute) / time.Minute)
}

// truncate shortens text to one line of at most n characters, ending on a
// whole word where it can.
func truncate(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	cut := string(r[:n-1])
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)*2/3 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

func orNone(s string) string {
	if s == "" {
		return "None"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func supersetText(groupLabel string) string {
	if groupLabel == "" {
		return "On its own"
	}
	return "In a superset"
}

// ordinal is "1st", "2nd", "3rd", "4th".
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

// supersetLabels numbers a list of exercises, lettering neighbours that share a
// group: ["", "", "g", "g"] is 1, 2, 3a, 3b. The phone numbers them the same way.
func supersetLabels(groups []string) []string {
	out := make([]string, 0, len(groups))
	number := 0
	for i := 0; i < len(groups); {
		j := i + 1
		for groups[i] != "" && j < len(groups) && groups[j] == groups[i] {
			j++
		}
		number++
		if j-i == 1 {
			out = append(out, strconv.Itoa(number))
		}
		for k := 0; j-i > 1 && k < j-i; k++ {
			out = append(out, fmt.Sprintf("%d%c", number, 'a'+k))
		}
		i = j
	}
	return out
}

func unitName(unit string) string {
	if unit == "lb" {
		return "pounds"
	}
	return "kilograms"
}

// readingText is "82.4 kg", "18.5% body fat" or both.
func readingText(weightKg, bodyFatPct *float64, displayUnit string) string {
	var parts []string
	if weightKg != nil {
		parts = append(parts, weightIn(*weightKg, displayUnit))
	}
	if bodyFatPct != nil {
		parts = append(parts, trimNum(fmt.Sprintf("%.1f", *bodyFatPct))+"% body fat")
	}
	return strings.Join(parts, ", ")
}

// weightIn is a kilogram value in the display unit to one decimal: "82.4 kg".
func weightIn(kg float64, displayUnit string) string {
	return maths.FormatTenths(displayKg(kg, displayUnit)) + " " + displayUnit
}

// sentence capitalises the first letter.
func sentence(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
