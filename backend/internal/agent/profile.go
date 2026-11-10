package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/maths"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// The user's body readings and settings: read them, log a reading, change a setting.

func (t *turn) getProfile(ctx context.Context, _ noInput) (any, error) {
	st, err := t.settings(ctx)
	if err != nil {
		return nil, err
	}
	readings, err := t.s.q.BodyMeasurementsFor(ctx, query.BodyMeasurementsForParams{UserID: t.userID, Limit: 20})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(readings))
	for i, m := range readings {
		item := map[string]any{"reading_id": m.ID.String(), "date": t.day(m.MeasuredAt), "from": m.Source}
		if m.WeightKg != nil {
			item["weight"] = weightIn(*m.WeightKg, t.displayUnit)
		}
		if m.BodyFatPct != nil {
			item["body_fat"] = trimNum(fmt.Sprintf("%.1f", *m.BodyFatPct)) + "%"
		}
		out[i] = item
	}
	settings := map[string]any{"totals_and_charts_in": st.DisplayUnit, "default_rest_s": st.RestDefaultS}
	if st.HeightCm != nil {
		settings["height_cm"] = *st.HeightCm
	}
	if st.BirthDate != "" {
		settings["birth_date"] = st.BirthDate
	}
	if st.Sex != "" {
		settings["sex"] = st.Sex
	}
	t.reading(ctx, "Read your body readings and settings")
	return map[string]any{"settings": settings, "body_readings_newest_first": out}, nil
}

// settings is the user's settings row, or the defaults before they have one.
func (t *turn) settings(ctx context.Context) (query.Setting, error) {
	st, err := t.s.q.GetSettingsFor(ctx, t.userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return query.Setting{UserID: t.userID, DisplayUnit: "kg", RestDefaultS: 60}, nil
	}
	return st, err
}

type logBodyInput struct {
	Weight     string   `json:"weight,omitempty" jsonschema_description:"Body weight as said, e.g. \"82.4\"."`
	Unit       string   `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb" jsonschema_description:"Defaults to the unit their totals are shown in."`
	BodyFatPct *float64 `json:"body_fat_pct,omitempty"`
	MeasuredAt string   `json:"measured_at,omitempty" jsonschema_description:"Local time, YYYY-MM-DDTHH:MM. Defaults to now."`
}

// logBody adds a body weight or body fat reading; applied straight away, with Undo.
func (t *turn) logBody(ctx context.Context, in logBodyInput) (any, error) {
	row := &milo.BodyMeasurement{Id: newID().String(), MeasuredAt: ts(t.now), Source: "manual", UpdatedAt: ts(t.now)}
	if in.MeasuredAt != "" {
		at, err := t.parseLocal(in.MeasuredAt, "measured_at")
		if err != nil {
			return nil, err
		}
		if at.After(t.now.Add(time.Minute)) {
			return nil, badInput("a reading can't be in the future")
		}
		row.MeasuredAt = ts(at)
	}
	if w := strings.TrimSpace(in.Weight); w != "" {
		unit := in.Unit
		if unit == "" {
			unit = t.displayUnit
		}
		kg, ok := maths.ToKg(w, unit)
		if !rows.ValidWeight(w) || !ok || kg < 20 || kg > 400 {
			return nil, badInput("weight %q %s doesn't look like a body weight", w, unit)
		}
		row.WeightKg = &kg
	}
	if in.BodyFatPct != nil {
		if *in.BodyFatPct < 2 || *in.BodyFatPct > 70 {
			return nil, badInput("body fat %.1f%% is out of range", *in.BodyFatPct)
		}
		row.BodyFatPct = in.BodyFatPct
	}
	if row.WeightKg == nil && row.BodyFatPct == nil {
		return nil, badInput("give a weight, a body fat percentage, or both")
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: "Logged " + readingText(row.WeightKg, row.BodyFatPct, t.displayUnit),
		Subtitle: "Body reading, " + t.day(row.MeasuredAt.AsTime()), Changes: []*milo.Change{rows.Change(row)}, AutoApply: true,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "reading_id": row.Id, "shown": doneNote}, nil
}

type settingsInput struct {
	DisplayUnit  string   `json:"display_unit,omitempty" jsonschema:"enum=kg,enum=lb" jsonschema_description:"The unit totals and charts are shown in. Logged weights keep their own unit."`
	RestDefaultS *int32   `json:"rest_default_s,omitempty" jsonschema_description:"The default rest timer, in seconds."`
	HeightCm     *float64 `json:"height_cm,omitempty"`
	BirthDate    *string  `json:"birth_date,omitempty" jsonschema_description:"YYYY-MM-DD; \"\" clears it."`
	Sex          *string  `json:"sex,omitempty" jsonschema:"enum=female,enum=male,enum=other,enum=" jsonschema_description:"\"\" clears it."`
}

// updateSettings changes the user's settings; applied straight away, with Undo.
func (t *turn) updateSettings(ctx context.Context, in settingsInput) (any, error) {
	st, err := t.settings(ctx)
	if err != nil {
		return nil, err
	}
	var said []string
	if u := in.DisplayUnit; u != "" && u != st.DisplayUnit {
		if u != "kg" && u != "lb" {
			return nil, badInput("display_unit must be kg or lb")
		}
		st.DisplayUnit = u
		said = append(said, "Totals and charts in "+unitName(u))
	}
	if r := in.RestDefaultS; r != nil && *r != st.RestDefaultS {
		if *r < 10 || *r > 900 {
			return nil, badInput("rest_default_s must be between 10 and 900 seconds")
		}
		st.RestDefaultS = *r
		said = append(said, "Rest timer "+durationText(int(*r)))
	}
	if h := in.HeightCm; h != nil {
		if *h < 50 || *h > 260 {
			return nil, badInput("height_cm must be between 50 and 260")
		}
		st.HeightCm = h
		said = append(said, fmt.Sprintf("Height %.0f cm", *h))
	}
	if b := in.BirthDate; b != nil && *b != st.BirthDate {
		if *b != "" {
			born, err := time.Parse(time.DateOnly, *b)
			if err != nil || born.After(t.now) {
				return nil, badInput("birth_date %q must be a past date, YYYY-MM-DD", *b)
			}
		}
		st.BirthDate = *b
		said = append(said, "Date of birth "+orNone(*b))
	}
	if s := in.Sex; s != nil && *s != st.Sex {
		st.Sex = *s
		said = append(said, "Sex "+orNone(*s))
	}
	if len(said) == 0 {
		return nil, badInput("nothing here differs from their current settings")
	}
	st.UpdatedAt = t.now
	title, subtitle := said[0], ""
	if len(said) > 1 {
		title, subtitle = "Settings updated", strings.Join(said, ", ")
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: title, Subtitle: subtitle,
		Changes: []*milo.Change{rows.Change(rows.Settings(st))}, AutoApply: true,
	})
	if err != nil {
		return nil, err
	}
	t.displayUnit = st.DisplayUnit
	return map[string]any{"card_id": id, "shown": doneNote}, nil
}

type exercisePrefsInput struct {
	ExerciseID string `json:"exercise_id"`
	Unit       string `json:"unit,omitempty" jsonschema:"enum=kg,enum=lb" jsonschema_description:"The unit this exercise is logged in from now on."`
	RestS      *int32 `json:"rest_s,omitempty" jsonschema_description:"This exercise's own rest timer in seconds; 0 goes back to the default."`
}

// setExercisePreferences sets the unit and rest timer one exercise uses.
func (t *turn) setExercisePreferences(ctx context.Context, in exercisePrefsInput) (any, error) {
	ex, err := t.exercise(ctx, in.ExerciseID)
	if err != nil {
		return nil, err
	}
	row := &milo.ExercisePref{ExerciseId: ex.ID.String(), Unit: rows.Parse[milo.WeightUnit](t.displayUnit)}
	stored, err := t.s.q.ExercisePrefFor(ctx, query.ExercisePrefForParams{UserID: t.userID, ExerciseID: ex.ID})
	if err == nil {
		row = rows.ExercisePref(stored)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	var said []string
	if u := rows.Parse[milo.WeightUnit](in.Unit); u != milo.WeightUnit_WEIGHT_UNIT_UNSPECIFIED && u != row.Unit {
		row.Unit = u
		said = append(said, "logged in "+unitName(in.Unit))
	}
	if r := in.RestS; r != nil {
		switch {
		case *r == 0 && row.RestS != nil:
			row.RestS = nil
			said = append(said, "rest back to the default")
		case *r < 0 || *r > 900:
			return nil, badInput("rest_s must be between 10 and 900 seconds, or 0 for the default")
		case *r > 0 && (row.RestS == nil || *row.RestS != *r):
			row.RestS = r
			said = append(said, "rest "+durationText(int(*r)))
		}
	}
	if len(said) == 0 {
		return nil, badInput("%s is already set that way", ex.Name)
	}
	row.UpdatedAt = ts(t.now)
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_DONE, Title: ex.Name, Subtitle: sentence(strings.Join(said, ", ")),
		Changes: []*milo.Change{rows.Change(row)}, AutoApply: true, ExerciseId: ex.ID.String(),
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": doneNote}, nil
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
