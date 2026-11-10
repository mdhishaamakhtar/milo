package syncsvc_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	milov1 "github.com/mdhishaamakhtar/milo/backend/gen/milo/v1"
	"github.com/mdhishaamakhtar/milo/backend/internal/apitest"
	"github.com/mdhishaamakhtar/milo/backend/internal/config"
)

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func setChange(id, weID uuid.UUID, weight string, updated time.Time, deleted bool) *milov1.Change {
	return &milov1.Change{Row: &milov1.Change_Set{Set: &milov1.Set{
		Id: id.String(), WorkoutExerciseId: weID.String(), Position: 1, Kind: milov1.SetKind_SET_KIND_NORMAL,
		Reps: proto.Int32(10), Weight: weight, WeightUnit: milov1.WeightUnit_WEIGHT_UNIT_LB, Done: true,
		UpdatedAt: ts(updated), Deleted: deleted,
	}}}
}

func workoutChange(id uuid.UUID, name string, updated time.Time) *milov1.Change {
	return &milov1.Change{Row: &milov1.Change_Workout{Workout: &milov1.Workout{
		Id: id.String(), Name: name, StartedAt: ts(apitest.At(0)), UpdatedAt: ts(updated),
	}}}
}

// pullAll follows has_more from cursor and returns every change and the final cursor.
func pullAll(t *testing.T, env *apitest.Env, ctx context.Context, cursor int64) ([]*milov1.Change, int64) {
	t.Helper()
	var all []*milov1.Change
	for {
		res, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.GetChanges()) > 500 {
			t.Fatalf("page of %d rows, want at most 500", len(res.GetChanges()))
		}
		all = append(all, res.GetChanges()...)
		cursor = res.GetCursor()
		if !res.GetHasMore() {
			return all, cursor
		}
	}
}

func setsByID(changes []*milov1.Change) map[string]*milov1.Set {
	out := map[string]*milov1.Set{}
	for _, c := range changes {
		if s := c.GetSet(); s != nil {
			out[s.GetId()] = s
		}
	}
	return out
}

func TestSyncFirstPullIncludesBuiltIns(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")

	changes, cursor := pullAll(t, env, ctx, 0)
	builtins, settings := 0, 0
	for _, c := range changes {
		if e := c.GetExercise(); e != nil && e.GetBuiltin() {
			builtins++
		}
		if c.GetSettings() != nil {
			settings++
		}
	}
	if builtins < 150 {
		t.Fatalf("first pull has %d built-in exercises, want at least 150", builtins)
	}
	if settings != 1 {
		t.Fatalf("first pull has %d settings rows, want 1", settings)
	}
	if cursor == 0 {
		t.Fatal("cursor didn't move")
	}
}

func TestSyncRoundTrip(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")
	_, cursor := pullAll(t, env, ctx, 0)

	exerciseID, workoutID, weID, setID, templateID, bodyID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := apitest.At(time.Hour)
	push := []*milov1.Change{
		{Row: &milov1.Change_Exercise{Exercise: &milov1.Exercise{
			Id: exerciseID.String(), Name: "Landmine Rotation", PrimaryMuscles: []string{"obliques"},
			Measurement: milov1.Measurement_MEASUREMENT_REPS_LOAD, Unilateral: true, Approximate: true,
			Met: proto.Float64(4.5), UpdatedAt: ts(now),
		}}},
		{Row: &milov1.Change_ExercisePref{ExercisePref: &milov1.ExercisePref{
			ExerciseId: exerciseID.String(), Unit: milov1.WeightUnit_WEIGHT_UNIT_LB, RestS: proto.Int32(90), UpdatedAt: ts(now),
		}}},
		workoutChange(workoutID, "Legs", now),
		{Row: &milov1.Change_WorkoutExercise{WorkoutExercise: &milov1.WorkoutExercise{
			Id: weID.String(), WorkoutId: workoutID.String(), ExerciseId: exerciseID.String(), Position: 1, GroupLabel: "4", UpdatedAt: ts(now),
		}}},
		setChange(setID, weID, "45", now, false),
		{Row: &milov1.Change_Template{Template: &milov1.Template{
			Id: templateID.String(), Name: "Pull day", UpdatedAt: ts(now), Items: []*milov1.TemplateItem{
				{ExerciseId: exerciseID.String(), GroupLabel: "4"}, {ExerciseId: uuid.NewString(), GroupLabel: "4"},
			},
		}}},
		{Row: &milov1.Change_BodyMeasurement{BodyMeasurement: &milov1.BodyMeasurement{
			Id: bodyID.String(), MeasuredAt: ts(now), WeightKg: proto.Float64(120.8), Source: "health", UpdatedAt: ts(now),
		}}},
		{Row: &milov1.Change_Settings{Settings: &milov1.Settings{
			DisplayUnit: milov1.WeightUnit_WEIGHT_UNIT_LB, RestDefaultS: 75, HeightCm: proto.Float64(183), BirthDate: "2001-03-12", UpdatedAt: ts(now),
		}}},
	}

	res, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Cursor: cursor, Changes: push})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetChanges()) != len(push) {
		t.Fatalf("pulled %d changes after pushing %d", len(res.GetChanges()), len(push))
	}
	for _, c := range res.GetChanges() {
		switch row := c.GetRow().(type) {
		case *milov1.Change_Set:
			if row.Set.GetWeight() != "45" || row.Set.GetWeightUnit() != milov1.WeightUnit_WEIGHT_UNIT_LB || row.Set.GetReps() != 10 {
				t.Fatalf("set came back as %v", row.Set)
			}
		case *milov1.Change_Exercise:
			if row.Exercise.GetBuiltin() || !row.Exercise.GetUnilateral() || !row.Exercise.GetApproximate() || row.Exercise.GetMet() != 4.5 {
				t.Fatalf("exercise came back as %v", row.Exercise)
			}
		case *milov1.Change_Template:
			if len(row.Template.GetItems()) != 2 || row.Template.GetItems()[0].GetGroupLabel() != "4" {
				t.Fatalf("template came back as %v", row.Template)
			}
		case *milov1.Change_Settings:
			if row.Settings.GetBirthDate() != "2001-03-12" || row.Settings.GetRestDefaultS() != 75 || row.Settings.GetHeightCm() != 183 {
				t.Fatalf("settings came back as %v", row.Settings)
			}
		case *milov1.Change_ExercisePref:
			if row.ExercisePref.GetRestS() != 90 || row.ExercisePref.GetUnit() != milov1.WeightUnit_WEIGHT_UNIT_LB {
				t.Fatalf("pref came back as %v", row.ExercisePref)
			}
		}
	}

	again, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Cursor: res.GetCursor()})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.GetChanges()) != 0 || again.GetCursor() != res.GetCursor() {
		t.Fatalf("a sync with nothing new returned %d changes and moved the cursor", len(again.GetChanges()))
	}
}

func TestSyncWeightKeepsWhatWasTyped(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")
	weID := uuid.New()
	weights := []string{"20.41", "15.875", "45", "0.5", "100"}
	var push []*milov1.Change
	ids := map[string]string{}
	for _, w := range weights {
		id := uuid.New()
		ids[id.String()] = w
		push = append(push, setChange(id, weID, w, apitest.At(0), false))
	}
	if _, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Changes: push}); err != nil {
		t.Fatal(err)
	}
	all, _ := pullAll(t, env, ctx, 0)
	for id, s := range setsByID(all) {
		if s.GetWeight() != ids[id] {
			t.Fatalf("weight %q came back as %q", ids[id], s.GetWeight())
		}
	}
}

func TestSyncPushOlderUpdateIgnored(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")
	setID, weID := uuid.New(), uuid.New()

	first, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Changes: []*milov1.Change{setChange(setID, weID, "40", apitest.At(2*time.Minute), false)}})
	if err != nil {
		t.Fatal(err)
	}
	_, cursor := pullAll(t, env, ctx, first.GetCursor())

	stale, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Cursor: cursor, Changes: []*milov1.Change{setChange(setID, weID, "30", apitest.At(time.Minute), false)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(stale.GetChanges()) != 0 {
		t.Fatalf("an older write produced %d changes", len(stale.GetChanges()))
	}
	all, _ := pullAll(t, env, ctx, 0)
	if got := setsByID(all)[setID.String()].GetWeight(); got != "40" {
		t.Fatalf("weight = %q, want the newer 40", got)
	}
}

func TestSyncTombstonePropagates(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")
	setID, weID := uuid.New(), uuid.New()
	res, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Changes: []*milov1.Change{setChange(setID, weID, "40", apitest.At(0), false)}})
	if err != nil {
		t.Fatal(err)
	}
	_, cursor := pullAll(t, env, ctx, res.GetCursor())

	del, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Cursor: cursor, Changes: []*milov1.Change{setChange(setID, weID, "40", apitest.At(time.Minute), true)}})
	if err != nil {
		t.Fatal(err)
	}
	got := setsByID(del.GetChanges())[setID.String()]
	if got == nil || !got.GetDeleted() {
		t.Fatalf("pull after delete = %v, want a tombstone for the set", del.GetChanges())
	}
}

func TestSyncPagination(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")
	weID := uuid.New()
	want := map[string]bool{}
	var push []*milov1.Change
	for i := range 1200 {
		id := uuid.New()
		want[id.String()] = true
		push = append(push, setChange(id, weID, "20", apitest.At(time.Duration(i)*time.Second), false))
	}
	if _, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Changes: push}); err != nil {
		t.Fatal(err)
	}

	all, _ := pullAll(t, env, ctx, 0)
	got := setsByID(all)
	if len(got) != len(want) {
		t.Fatalf("paged pull returned %d sets, want %d", len(got), len(want))
	}
	seen := map[string]bool{}
	for _, c := range all {
		if s := c.GetSet(); s != nil {
			if seen[s.GetId()] {
				t.Fatalf("set %s came back twice", s.GetId())
			}
			seen[s.GetId()] = true
		}
	}
}

func TestSyncRejectsForeignRow(t *testing.T) {
	env := apitest.New(t, config.Config{})
	alice, _ := env.SignUp(t, "alice")
	bob, _ := env.SignUp(t, "bob")
	workoutID := uuid.New()

	if _, err := env.Sync.Sync(alice, &milov1.SyncRequest{Changes: []*milov1.Change{workoutChange(workoutID, "Alice's legs", apitest.At(0))}}); err != nil {
		t.Fatal(err)
	}
	_, err := env.Sync.Sync(bob, &milov1.SyncRequest{Changes: []*milov1.Change{workoutChange(workoutID, "Bob was here", apitest.At(time.Hour))}})
	apitest.WantCode(t, err, connect.CodePermissionDenied)

	all, _ := pullAll(t, env, alice, 0)
	var bench *milov1.Exercise
	for _, c := range all {
		if w := c.GetWorkout(); w != nil && w.GetName() != "Alice's legs" {
			t.Fatalf("alice's workout was renamed to %q", w.GetName())
		}
		if e := c.GetExercise(); e != nil && e.GetName() == "Bench Press (Barbell)" {
			bench = e
		}
	}
	bobAll, _ := pullAll(t, env, bob, 0)
	for _, c := range bobAll {
		if c.GetWorkout() != nil {
			t.Fatal("bob can see alice's workout")
		}
	}

	if bench == nil {
		t.Fatal("no Bench Press (Barbell) built-in")
	}
	for _, builtin := range []bool{false, true} {
		edit := proto.Clone(bench).(*milov1.Exercise)
		edit.Name, edit.Builtin, edit.UpdatedAt = "My bench", builtin, ts(time.Now().Add(time.Hour))
		_, err = env.Sync.Sync(bob, &milov1.SyncRequest{Changes: []*milov1.Change{{Row: &milov1.Change_Exercise{Exercise: edit}}}})
		apitest.WantCode(t, err, connect.CodePermissionDenied)
	}
}

func TestSyncRejectsBadInputAndRollsBack(t *testing.T) {
	env := apitest.New(t, config.Config{})
	ctx, _ := env.SignUp(t, "hishaam")
	workoutID := uuid.New()

	_, err := env.Sync.Sync(ctx, &milov1.SyncRequest{Changes: []*milov1.Change{
		workoutChange(workoutID, "Legs", apitest.At(0)),
		setChange(uuid.New(), uuid.New(), "forty", apitest.At(0), false),
	}})
	apitest.WantCode(t, err, connect.CodeInvalidArgument)

	_, err = env.Sync.Sync(ctx, &milov1.SyncRequest{Changes: []*milov1.Change{
		{Row: &milov1.Change_Workout{Workout: &milov1.Workout{Id: uuid.NewString(), StartedAt: ts(apitest.At(0))}}},
	}})
	apitest.WantCode(t, err, connect.CodeInvalidArgument)

	all, _ := pullAll(t, env, ctx, 0)
	for _, c := range all {
		if c.GetWorkout() != nil {
			t.Fatal("a rejected push left a workout behind")
		}
	}
}

func TestSyncNeedsSignIn(t *testing.T) {
	env := apitest.New(t, config.Config{})
	_, err := env.Sync.Sync(context.Background(), &milov1.SyncRequest{})
	apitest.WantCode(t, err, connect.CodeUnauthenticated)
}
