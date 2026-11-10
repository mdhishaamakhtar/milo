package syncsvc

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/dbq"
)

// Conversions between proto rows and database rows, with validation of what a
// client sends. Invalid input is a CodeInvalidArgument error naming the row.

func invalid(format string, args ...any) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf(format, args...))
}

func parseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, invalid("%s id %q is not a UUID", what, s)
	}
	return id, nil
}

func parseOptID(s, what string) (*uuid.UUID, error) {
	if s == "" {
		return nil, nil
	}
	id, err := parseID(s, what)
	return &id, err
}

func requiredTime(t *timestamppb.Timestamp, what string) (time.Time, error) {
	if t == nil || !t.IsValid() {
		return time.Time{}, invalid("%s needs a valid timestamp", what)
	}
	return t.AsTime(), nil
}

func optTime(t *timestamppb.Timestamp) *time.Time {
	if t == nil || !t.IsValid() {
		return nil
	}
	v := t.AsTime()
	return &v
}

func tsOrNil(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func idOrEmpty(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

// tombstone returns the deleted_at to store: the write's own time when deleted.
func tombstone(deleted bool, updatedAt time.Time) *time.Time {
	if !deleted {
		return nil
	}
	return &updatedAt
}

var weightPattern = regexp.MustCompile(`^\d{1,6}(\.\d{1,3})?$`)

func checkWeight(w string) error {
	if w != "" && !weightPattern.MatchString(w) {
		return invalid("weight %q must be a plain decimal with at most 3 places", w)
	}
	return nil
}

// trimDecimal turns Postgres's "45.000" back into "45" and "20.410" into "20.41".
func trimDecimal(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// Enums.

var measurementToDB = map[milov1.Measurement]string{
	milov1.Measurement_MEASUREMENT_REPS_LOAD:  "reps_load",
	milov1.Measurement_MEASUREMENT_BODYWEIGHT: "bodyweight",
	milov1.Measurement_MEASUREMENT_ASSISTED:   "assisted",
	milov1.Measurement_MEASUREMENT_TIMED:      "timed",
	milov1.Measurement_MEASUREMENT_DISTANCE:   "distance",
}

var setKindToDB = map[milov1.SetKind]string{
	milov1.SetKind_SET_KIND_NORMAL:  "normal",
	milov1.SetKind_SET_KIND_WARMUP:  "warmup",
	milov1.SetKind_SET_KIND_DROP:    "drop",
	milov1.SetKind_SET_KIND_FAILURE: "failure",
}

var unitToDB = map[milov1.WeightUnit]string{
	milov1.WeightUnit_WEIGHT_UNIT_KG: "kg",
	milov1.WeightUnit_WEIGHT_UNIT_LB: "lb",
}

func reverse[K comparable, V comparable](m map[K]V) map[V]K {
	r := make(map[V]K, len(m))
	for k, v := range m {
		r[v] = k
	}
	return r
}

var (
	measurementFromDB = reverse(measurementToDB)
	setKindFromDB     = reverse(setKindToDB)
	unitFromDB        = reverse(unitToDB)
)

// Unspecified enums take the default instead of failing, so older clients keep working.

func measurement(m milov1.Measurement) string {
	if v, ok := measurementToDB[m]; ok {
		return v
	}
	return "reps_load"
}

func setKind(k milov1.SetKind) string {
	if v, ok := setKindToDB[k]; ok {
		return v
	}
	return "normal"
}

func unit(u milov1.WeightUnit) string {
	if v, ok := unitToDB[u]; ok {
		return v
	}
	return "kg"
}

// Templates store their items as JSON.

type templateItem struct {
	ExerciseID string `json:"exercise_id"`
	GroupLabel string `json:"group_label"`
}

func templateItemsJSON(items []*milov1.TemplateItem) ([]byte, error) {
	out := make([]templateItem, 0, len(items))
	for _, it := range items {
		if _, err := parseID(it.GetExerciseId(), "template item exercise"); err != nil {
			return nil, err
		}
		out = append(out, templateItem{ExerciseID: it.GetExerciseId(), GroupLabel: it.GetGroupLabel()})
	}
	return json.Marshal(out)
}

func templateItemsProto(raw []byte) []*milov1.TemplateItem {
	var items []templateItem
	_ = json.Unmarshal(raw, &items)
	out := make([]*milov1.TemplateItem, 0, len(items))
	for _, it := range items {
		out = append(out, &milov1.TemplateItem{ExerciseId: it.ExerciseID, GroupLabel: it.GroupLabel})
	}
	return out
}

// Database rows to proto.

func exerciseProto(e dbq.Exercise) *milov1.Exercise {
	return &milov1.Exercise{
		Id:               e.ID.String(),
		Builtin:          e.UserID == nil,
		Name:             e.Name,
		Aliases:          e.Aliases,
		PrimaryMuscles:   e.PrimaryMuscles,
		SecondaryMuscles: e.SecondaryMuscles,
		MovementPattern:  e.MovementPattern,
		Equipment:        e.Equipment,
		Measurement:      measurementFromDB[e.Measurement],
		Unilateral:       e.Unilateral,
		Met:              e.Met,
		Description:      e.Description,
		Approximate:      e.Approximate,
		UpdatedAt:        timestamppb.New(e.UpdatedAt),
		Deleted:          e.DeletedAt != nil,
	}
}

func exercisePrefProto(p dbq.ExercisePref) *milov1.ExercisePref {
	return &milov1.ExercisePref{
		ExerciseId: p.ExerciseID.String(),
		Unit:       unitFromDB[p.Unit],
		RestS:      p.RestS,
		UpdatedAt:  timestamppb.New(p.UpdatedAt),
		Deleted:    p.DeletedAt != nil,
	}
}

func settingsProto(s dbq.PullSettingsRow) *milov1.Settings {
	return &milov1.Settings{
		DisplayUnit:  unitFromDB[s.DisplayUnit],
		RestDefaultS: s.RestDefaultS,
		HeightCm:     s.HeightCm,
		BirthDate:    s.BirthDate,
		Sex:          s.Sex,
		UpdatedAt:    timestamppb.New(s.UpdatedAt),
	}
}

func bodyMeasurementProto(b dbq.BodyMeasurement) *milov1.BodyMeasurement {
	return &milov1.BodyMeasurement{
		Id:         b.ID.String(),
		MeasuredAt: timestamppb.New(b.MeasuredAt),
		WeightKg:   b.WeightKg,
		BodyFatPct: b.BodyFatPct,
		Source:     b.Source,
		UpdatedAt:  timestamppb.New(b.UpdatedAt),
		Deleted:    b.DeletedAt != nil,
	}
}

func workoutProto(w dbq.Workout) *milov1.Workout {
	return &milov1.Workout{
		Id:         w.ID.String(),
		Name:       w.Name,
		Notes:      w.Notes,
		StartedAt:  timestamppb.New(w.StartedAt),
		EndedAt:    tsOrNil(w.EndedAt),
		TemplateId: idOrEmpty(w.TemplateID),
		HealthUuid: idOrEmpty(w.HealthUuid),
		UpdatedAt:  timestamppb.New(w.UpdatedAt),
		Deleted:    w.DeletedAt != nil,
	}
}

func workoutExerciseProto(we dbq.WorkoutExercise) *milov1.WorkoutExercise {
	return &milov1.WorkoutExercise{
		Id:         we.ID.String(),
		WorkoutId:  we.WorkoutID.String(),
		ExerciseId: we.ExerciseID.String(),
		Position:   we.Position,
		GroupLabel: we.GroupLabel,
		Notes:      we.Notes,
		UpdatedAt:  timestamppb.New(we.UpdatedAt),
		Deleted:    we.DeletedAt != nil,
	}
}

func setProto(s dbq.PullSetsRow) *milov1.Set {
	return &milov1.Set{
		Id:                s.ID.String(),
		WorkoutExerciseId: s.WorkoutExerciseID.String(),
		Position:          s.Position,
		Kind:              setKindFromDB[s.Kind],
		Reps:              s.Reps,
		Weight:            trimDecimal(s.Weight),
		WeightUnit:        unitFromDB[s.WeightUnit],
		PerSide:           s.PerSide,
		DurationS:         s.DurationS,
		DistanceM:         s.DistanceM,
		Rpe:               s.Rpe,
		Done:              s.Done,
		UpdatedAt:         timestamppb.New(s.UpdatedAt),
		Deleted:           s.DeletedAt != nil,
	}
}

func templateProto(t dbq.Template) *milov1.Template {
	return &milov1.Template{
		Id:        t.ID.String(),
		Name:      t.Name,
		Notes:     t.Notes,
		Items:     templateItemsProto(t.Items),
		UpdatedAt: timestamppb.New(t.UpdatedAt),
		Deleted:   t.DeletedAt != nil,
	}
}
