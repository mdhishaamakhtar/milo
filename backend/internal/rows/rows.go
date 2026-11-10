// Package rows turns database rows into API messages, the one place both the
// sync service and the agent do it.
package rows

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
)

// Enums are stored by their lower-case name without the type prefix:
// MEASUREMENT_REPS_LOAD is stored as "reps_load".

// Name is how an API enum value is stored; "" for the unspecified value.
func Name(e protoreflect.Enum) string {
	values := e.Descriptor().Values()
	if e.Number() == 0 || values.ByNumber(e.Number()) == nil {
		return ""
	}
	prefix := strings.TrimSuffix(string(values.ByNumber(0).Name()), "UNSPECIFIED")
	return strings.ToLower(strings.TrimPrefix(string(values.ByNumber(e.Number()).Name()), prefix))
}

// Parse reads a stored enum name back; unknown names give the unspecified value.
func Parse[E interface {
	~int32
	protoreflect.Enum
}](name string) E {
	var zero E
	values := zero.Descriptor().Values()
	prefix := strings.TrimSuffix(string(values.ByNumber(0).Name()), "UNSPECIFIED")
	if v := values.ByName(protoreflect.Name(prefix + strings.ToUpper(name))); v != nil {
		return E(v.Number())
	}
	return zero
}

var weightPattern = regexp.MustCompile(`^(\d{1,6}(\.\d{1,3})?)?$`)

// ValidWeight reports whether a typed weight is a plain decimal ("" for none).
func ValidWeight(w string) bool { return weightPattern.MatchString(w) }

func Exercise(e query.Exercise) *milo.Exercise {
	return &milo.Exercise{
		Id: e.ID.String(), Builtin: e.UserID == nil, Name: e.Name, Aliases: e.Aliases,
		PrimaryMuscles: e.PrimaryMuscles, SecondaryMuscles: e.SecondaryMuscles,
		MovementPattern: e.MovementPattern, Equipment: e.Equipment,
		Measurement: Parse[milo.Measurement](e.Measurement), Unilateral: e.Unilateral, Met: e.Met,
		Description: e.Description, Approximate: e.Approximate,
		UpdatedAt: timestamppb.New(e.UpdatedAt), Deleted: e.DeletedAt != nil,
	}
}

func ExercisePref(p query.ExercisePref) *milo.ExercisePref {
	return &milo.ExercisePref{
		ExerciseId: p.ExerciseID.String(), Unit: Parse[milo.WeightUnit](p.Unit), RestS: p.RestS,
		UpdatedAt: timestamppb.New(p.UpdatedAt), Deleted: p.DeletedAt != nil,
	}
}

func Settings(s query.Setting) *milo.Settings {
	return &milo.Settings{
		DisplayUnit: Parse[milo.WeightUnit](s.DisplayUnit), RestDefaultS: s.RestDefaultS, HeightCm: s.HeightCm,
		BirthDate: s.BirthDate, Sex: s.Sex, UpdatedAt: timestamppb.New(s.UpdatedAt),
	}
}

func BodyMeasurement(b query.BodyMeasurement) *milo.BodyMeasurement {
	return &milo.BodyMeasurement{
		Id: b.ID.String(), MeasuredAt: timestamppb.New(b.MeasuredAt), WeightKg: b.WeightKg, BodyFatPct: b.BodyFatPct,
		Source: b.Source, UpdatedAt: timestamppb.New(b.UpdatedAt), Deleted: b.DeletedAt != nil,
	}
}

func Workout(w query.Workout) *milo.Workout {
	out := &milo.Workout{
		Id: w.ID.String(), Name: w.Name, Notes: w.Notes, StartedAt: timestamppb.New(w.StartedAt),
		UpdatedAt: timestamppb.New(w.UpdatedAt), Deleted: w.DeletedAt != nil,
	}
	if w.EndedAt != nil {
		out.EndedAt = timestamppb.New(*w.EndedAt)
	}
	if w.TemplateID != nil {
		out.TemplateId = w.TemplateID.String()
	}
	if w.HealthUuid != nil {
		out.HealthUuid = w.HealthUuid.String()
	}
	return out
}

func WorkoutExercise(we query.WorkoutExercise) *milo.WorkoutExercise {
	return &milo.WorkoutExercise{
		Id: we.ID.String(), WorkoutId: we.WorkoutID.String(), ExerciseId: we.ExerciseID.String(),
		Position: we.Position, GroupLabel: we.GroupLabel, Notes: we.Notes,
		UpdatedAt: timestamppb.New(we.UpdatedAt), Deleted: we.DeletedAt != nil,
	}
}

func Set(s query.Set) *milo.Set {
	return &milo.Set{
		Id: s.ID.String(), WorkoutExerciseId: s.WorkoutExerciseID.String(), Position: s.Position,
		Kind: Parse[milo.SetKind](s.Kind), Reps: s.Reps, Weight: s.Weight, WeightUnit: Parse[milo.WeightUnit](s.WeightUnit),
		PerSide: s.PerSide, DurationS: s.DurationS, DistanceM: s.DistanceM, Rpe: s.Rpe, Done: s.Done,
		UpdatedAt: timestamppb.New(s.UpdatedAt), Deleted: s.DeletedAt != nil,
	}
}

func Template(t query.Template) *milo.Template {
	return &milo.Template{
		Id: t.ID.String(), Name: t.Name, Notes: t.Notes, Items: TemplateItems(t.Items),
		UpdatedAt: timestamppb.New(t.UpdatedAt), Deleted: t.DeletedAt != nil,
	}
}

// Template items are stored as JSON: [{"exercise_id": "...", "group_label": "4"}].
type templateItem struct {
	ExerciseID string `json:"exercise_id"`
	GroupLabel string `json:"group_label"`
}

func TemplateItems(raw []byte) []*milo.TemplateItem {
	var items []templateItem
	_ = json.Unmarshal(raw, &items)
	out := make([]*milo.TemplateItem, len(items))
	for i, it := range items {
		out[i] = &milo.TemplateItem{ExerciseId: it.ExerciseID, GroupLabel: it.GroupLabel}
	}
	return out
}

func TemplateItemsJSON(items []*milo.TemplateItem) ([]byte, error) {
	out := make([]templateItem, len(items))
	for i, it := range items {
		out[i] = templateItem{ExerciseID: it.GetExerciseId(), GroupLabel: it.GetGroupLabel()}
	}
	return json.Marshal(out)
}

// Change wraps any row message in a Change.
func Change(row any) *milo.Change {
	switch r := row.(type) {
	case *milo.Exercise:
		return &milo.Change{Row: &milo.Change_Exercise{Exercise: r}}
	case *milo.ExercisePref:
		return &milo.Change{Row: &milo.Change_ExercisePref{ExercisePref: r}}
	case *milo.Settings:
		return &milo.Change{Row: &milo.Change_Settings{Settings: r}}
	case *milo.BodyMeasurement:
		return &milo.Change{Row: &milo.Change_BodyMeasurement{BodyMeasurement: r}}
	case *milo.Workout:
		return &milo.Change{Row: &milo.Change_Workout{Workout: r}}
	case *milo.WorkoutExercise:
		return &milo.Change{Row: &milo.Change_WorkoutExercise{WorkoutExercise: r}}
	case *milo.Set:
		return &milo.Change{Row: &milo.Change_Set{Set: r}}
	case *milo.Template:
		return &milo.Change{Row: &milo.Change_Template{Template: r}}
	}
	panic(fmt.Sprintf("rows.Change: %T is not a row", row))
}
