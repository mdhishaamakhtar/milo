package agent

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/db/query"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

// turn is one user message being answered: who, where, and what Milo has
// proposed so far in it.
type turn struct {
	s           *Service
	userID      uuid.UUID
	convID      uuid.UUID
	mctx        *milo.MiloContext
	loc         *time.Location
	now         time.Time
	displayUnit string
	emit        func(*milo.SendResponse) error

	// What earlier tool calls in this turn proposed. The phone applies cards,
	// so the database doesn't have these rows yet; later calls build on them here.
	workouts map[uuid.UUID]workout        // started, created or finished in this turn
	slots    map[uuid.UUID][]*slot        // workout → its exercises, once a tool has touched it
	proposed map[uuid.UUID]query.Exercise // new exercises, usable before the user adds them
}

func newTurn(s *Service, userID, convID uuid.UUID, mctx *milo.MiloContext, loc *time.Location, displayUnit string, send func(*milo.SendResponse) error) *turn {
	// Reads run side by side and each reports what it read, so the stream takes one at a time.
	var sending sync.Mutex
	emit := func(r *milo.SendResponse) error {
		sending.Lock()
		defer sending.Unlock()
		return send(r)
	}
	return &turn{
		s: s, userID: userID, convID: convID, mctx: mctx, loc: loc, now: s.now(), displayUnit: displayUnit, emit: emit,
		workouts: map[uuid.UUID]workout{}, slots: map[uuid.UUID][]*slot{}, proposed: map[uuid.UUID]query.Exercise{},
	}
}

// day is "Tue 6 Oct" in the user's time zone, with the year when it isn't this one.
func (t *turn) day(at time.Time) string {
	return dayText(at.In(t.loc), t.now.In(t.loc))
}

// workout is where new sets and exercises go.
type workout struct {
	id        uuid.UUID
	name      string
	startedAt time.Time
	running   bool
	created   bool // in this turn, so not in the database
}

func workoutOf(w query.Workout) workout {
	return workout{id: w.ID, name: w.Name, startedAt: w.StartedAt, running: w.EndedAt == nil}
}

// slot is one exercise in one workout, with its sets as this turn leaves them.
type slot struct {
	we      *milo.WorkoutExercise
	sets    []*milo.Set
	created bool // in this turn
}

// resolveWorkout finds the workout a tool call means: the given id, else the
// one on screen, else the one running.
func (t *turn) resolveWorkout(ctx context.Context, workoutID string) (workout, error) {
	if workoutID == "" {
		workoutID = t.mctx.GetWorkoutId()
	}
	if workoutID == "" {
		w, ok, err := t.runningWorkout(ctx)
		if err != nil || ok {
			return w, err
		}
		return workout{}, badInput("no workout is open or running: pass workout_id, start one with start_workout, or use log_sets with new_workout for a session that already happened")
	}
	id, err := parseUUID(workoutID, "workout_id")
	if err != nil {
		return workout{}, err
	}
	if w, ok := t.workouts[id]; ok {
		return w, nil
	}
	w, err := t.userWorkout(ctx, id)
	if err != nil {
		return workout{}, err
	}
	return workoutOf(w), nil
}

// runningWorkout is the workout in progress, counting one started or finished in this turn.
func (t *turn) runningWorkout(ctx context.Context) (workout, bool, error) {
	for _, w := range t.workouts {
		if w.created && w.running {
			return w, true, nil
		}
	}
	row, err := t.s.q.RunningWorkout(ctx, t.userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return workout{}, false, nil
	}
	if err != nil {
		return workout{}, false, err
	}
	if w, ok := t.workouts[row.ID]; ok {
		return w, w.running, nil
	}
	return workoutOf(row), true, nil
}

// slot returns where an exercise's sets go in a workout: the exercise if it's
// already there, else a new one, with the row that creates it.
func (t *turn) slot(ctx context.Context, w workout, exerciseID uuid.UUID) (*slot, []*milo.Change, error) {
	slots, err := t.slotsOf(ctx, w)
	if err != nil {
		return nil, nil, err
	}
	for _, sl := range slices.Backward(slots) {
		if sl.we.GetExerciseId() == exerciseID.String() {
			return sl, nil, nil
		}
	}
	return t.newSlot(ctx, w, exerciseID, "")
}

// newSlot adds an exercise to the end of a workout.
func (t *turn) newSlot(ctx context.Context, w workout, exerciseID uuid.UUID, groupLabel string) (*slot, []*milo.Change, error) {
	slots, err := t.slotsOf(ctx, w)
	if err != nil {
		return nil, nil, err
	}
	position := int32(1)
	if len(slots) > 0 {
		position = slots[len(slots)-1].we.GetPosition() + 1
	}
	sl := &slot{created: true, we: &milo.WorkoutExercise{
		Id: newID().String(), WorkoutId: w.id.String(), ExerciseId: exerciseID.String(),
		Position: position, GroupLabel: groupLabel, UpdatedAt: ts(t.now),
	}}
	t.slots[w.id] = append(slots, sl)
	return sl, []*milo.Change{rows.Change(sl.we)}, nil
}

// slotsOf is a workout's exercises in order, as this turn leaves them. The
// first call reads the database; after that the turn's own view is the truth.
func (t *turn) slotsOf(ctx context.Context, w workout) ([]*slot, error) {
	if slots, ok := t.slots[w.id]; ok || w.created {
		return slots, nil
	}
	wes, err := t.s.q.WorkoutExercisesFor(ctx, query.WorkoutExercisesForParams{UserID: t.userID, WorkoutIds: []uuid.UUID{w.id}})
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(wes))
	slots := make([]*slot, len(wes))
	byID := map[uuid.UUID]*slot{}
	for i, we := range wes {
		ids[i] = we.ID
		slots[i] = &slot{we: &milo.WorkoutExercise{
			Id: we.ID.String(), WorkoutId: we.WorkoutID.String(), ExerciseId: we.ExerciseID.String(),
			Position: we.Position, GroupLabel: we.GroupLabel, Notes: we.Notes, UpdatedAt: ts(we.UpdatedAt),
		}}
		byID[we.ID] = slots[i]
	}
	sets, err := t.s.q.SetsFor(ctx, query.SetsForParams{UserID: t.userID, WeIds: ids})
	if err != nil {
		return nil, err
	}
	for _, s := range sets {
		byID[s.WorkoutExerciseID].sets = append(byID[s.WorkoutExerciseID].sets, rows.Set(s))
	}
	t.slots[w.id] = slots
	return slots, nil
}

// Lookups scoped to the user. A missing row is the model's mistake, so the
// error goes back to it.

func (t *turn) visibleExercise(ctx context.Context, id uuid.UUID) (query.Exercise, error) {
	if ex, ok := t.proposed[id]; ok {
		return ex, nil
	}
	ex, err := t.s.q.VisibleExercise(ctx, query.VisibleExerciseParams{ID: id, UserID: &t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ex, badInput("no exercise %s in this user's library; search first, or propose a new exercise", id)
	}
	return ex, err
}

// exercise reads an exercise by the id the model gave.
func (t *turn) exercise(ctx context.Context, id string) (query.Exercise, error) {
	exerciseID, err := parseUUID(id, "exercise_id")
	if err != nil {
		return query.Exercise{}, err
	}
	return t.visibleExercise(ctx, exerciseID)
}

func (t *turn) userWorkout(ctx context.Context, id uuid.UUID) (query.Workout, error) {
	w, err := t.s.q.GetUserWorkout(ctx, query.GetUserWorkoutParams{ID: id, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return w, badInput("no workout %s in this user's log", id)
	}
	return w, err
}

func (t *turn) userWorkoutExercise(ctx context.Context, id string) (query.WorkoutExercise, error) {
	weID, err := parseUUID(id, "workout_exercise_id")
	if err != nil {
		return query.WorkoutExercise{}, err
	}
	we, err := t.s.q.GetUserWorkoutExercise(ctx, query.GetUserWorkoutExerciseParams{ID: weID, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return we, badInput("no workout exercise %s in this user's log", weID)
	}
	return we, err
}

func (t *turn) userTemplate(ctx context.Context, id string) (query.Template, error) {
	templateID, err := parseUUID(id, "template_id")
	if err != nil {
		return query.Template{}, err
	}
	tpl, err := t.s.q.GetUserTemplate(ctx, query.GetUserTemplateParams{ID: templateID, UserID: t.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return tpl, badInput("no template %s; list_templates shows the ones they have", templateID)
	}
	return tpl, err
}

func parseUUID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, badInput("%s %q is not a valid id", what, s)
	}
	return id, nil
}

// newID is a time-ordered id for rows Milo proposes.
func newID() uuid.UUID {
	if id, err := uuid.NewV7(); err == nil {
		return id
	}
	return uuid.New()
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

// version is what a card says it was based on, so the phone can refuse a stale one.
func version(table string, id uuid.UUID, updatedAt time.Time) *milo.RowVersion {
	return &milo.RowVersion{Table: table, Id: id.String(), UpdatedAt: ts(updatedAt)}
}

// What the app shows when a conversation is reopened, stored as it streams.

type textPayload struct {
	Text string `json:"text"`
}

type readingPayload struct {
	Summary string `json:"summary"`
}

func (t *turn) saveItem(ctx context.Context, kind string, payload []byte, cardID *uuid.UUID, status *string) error {
	_, err := t.s.q.InsertMiloItem(ctx, query.InsertMiloItemParams{
		ConversationID: t.convID, UserID: t.userID, Kind: kind, Payload: payload, CardID: cardID, CardStatus: status,
	})
	return err
}

func (t *turn) saveText(ctx context.Context, kind, text string) {
	b, _ := json.Marshal(textPayload{Text: text})
	if err := t.saveItem(ctx, kind, b, nil, nil); err != nil {
		t.s.log.Error("agent: save text", "err", err, "conversation", t.convID)
	}
}

// reading tells the user what Milo looked at.
func (t *turn) reading(ctx context.Context, summary string) {
	b, _ := json.Marshal(readingPayload{Summary: summary})
	if err := t.saveItem(ctx, "reading", b, nil, nil); err != nil {
		t.s.log.Error("agent: save reading", "err", err, "conversation", t.convID)
	}
	_ = t.emit(&milo.SendResponse{Event: &milo.SendResponse_Reading{Reading: &milo.Reading{Summary: summary}}})
}

// emitCard stores a card as pending and streams it to the app.
func (t *turn) emitCard(ctx context.Context, card *milo.Card) (string, error) {
	id := newID()
	card.Id = id.String()
	card.Status = milo.CardStatus_CARD_STATUS_PENDING
	card.AppliedTitle = cmp.Or(card.AppliedTitle, card.Title)
	b, err := protojson.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("encode card: %w", err)
	}
	status := "pending"
	if err := t.saveItem(ctx, "card", b, &id, &status); err != nil {
		return "", fmt.Errorf("save card: %w", err)
	}
	if err := t.emit(&milo.SendResponse{Event: &milo.SendResponse_Card{Card: card}}); err != nil {
		return "", err
	}
	return id.String(), nil
}

// persistMessages appends a finished turn to the model's history in one go, so
// a failed turn leaves no half-written exchange behind.
func (s *Service) persistMessages(ctx context.Context, convID, userID uuid.UUID, msgs []anthropic.MessageParam) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
		if err := q.InsertAgentMessage(ctx, query.InsertAgentMessageParams{ConversationID: convID, UserID: userID, Role: roleOf(m), Content: b}); err != nil {
			return err
		}
	}
	if err := q.MarkCardsReported(ctx, convID); err != nil {
		return err
	}
	if err := q.TouchConversation(ctx, convID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) addUsage(ctx context.Context, userID uuid.UUID, u anthropic.Usage) {
	err := s.q.AddUsage(ctx, query.AddUsageParams{
		UserID: userID, Day: s.now().UTC().Truncate(24 * time.Hour),
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CacheReadTokens: u.CacheReadInputTokens, CacheWriteTokens: u.CacheCreationInputTokens,
	})
	if err != nil {
		s.log.Error("agent: record usage", "err", err)
	}
}

func roleOf(m anthropic.MessageParam) string {
	if m.OfAssistant != nil {
		return "assistant"
	}
	return "user"
}
