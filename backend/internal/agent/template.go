package agent

import (
	"context"
	"strings"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type templateInput struct {
	TemplateID string              `json:"template_id,omitempty" jsonschema_description:"To change a template they already have. Leave out for a new one."`
	Name       string              `json:"name,omitempty" jsonschema_description:"Required for a new template; for an existing one, only to rename it."`
	Notes      *string             `json:"notes,omitempty"`
	Items      []templateItemInput `json:"items,omitempty" jsonschema_description:"The whole list in order. Required for a new template; for an existing one, leave out to keep its list."`
}

type templateItemInput struct {
	ExerciseID string `json:"exercise_id"`
	GroupLabel string `json:"group_label,omitempty" jsonschema_description:"Supersets: neighbouring exercises with the same label are done back to back, shown as 4a and 4b."`
	Note       string `json:"note,omitempty" jsonschema_description:"A word or two for the card, e.g. \"fresh\", \"volume\"."`
}

// proposeTemplate drafts a template, or a change to one: exercises in order,
// supersets grouped, no numbers. The user saves it.
func (t *turn) proposeTemplate(ctx context.Context, in templateInput) (any, error) {
	tpl := &milo.Template{Id: newID().String()}
	card := &milo.Card{Kind: milo.CardKind_CARD_KIND_TEMPLATE_DRAFT}
	before := map[string]bool{} // exercises already in the template being changed
	if in.TemplateID != "" {
		stored, err := t.userTemplate(ctx, in.TemplateID)
		if err != nil {
			return nil, err
		}
		tpl = rows.Template(stored)
		for _, it := range tpl.Items {
			before[it.GetExerciseId()] = true
		}
		card.Base = []*milo.RowVersion{version("template", stored.ID, stored.UpdatedAt)}
		card.ApplyLabel = "Save changes"
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		tpl.Name = name
	}
	if in.Notes != nil {
		tpl.Notes = strings.TrimSpace(*in.Notes)
	}
	notes := map[string]string{}
	if len(in.Items) > 0 {
		tpl.Items = make([]*milo.TemplateItem, len(in.Items))
		for i, it := range in.Items {
			tpl.Items[i] = &milo.TemplateItem{ExerciseId: it.ExerciseID, GroupLabel: it.GroupLabel}
			notes[it.ExerciseID] = it.Note
		}
	}
	if tpl.Name == "" || len(tpl.Items) == 0 {
		return nil, badInput("a template needs a name and at least one exercise")
	}

	groups := make([]string, len(tpl.Items))
	for i, it := range tpl.Items {
		groups[i] = it.GetGroupLabel()
	}
	labels := supersetLabels(groups)
	for i, it := range tpl.Items {
		ex, err := t.exercise(ctx, it.GetExerciseId())
		if err != nil {
			return nil, err
		}
		card.Changes = append(card.Changes, t.pending(ex.ID)...)
		note := notes[it.GetExerciseId()]
		if len(before) > 0 && !before[it.GetExerciseId()] && note == "" {
			note = "new"
		}
		card.Lines = append(card.Lines, &milo.CardLine{Label: labels[i], After: ex.Name, Before: note})
	}
	tpl.UpdatedAt = ts(t.now)
	card.Changes = append(card.Changes, rows.Change(tpl))
	card.Title = tpl.Name
	card.Subtitle = "Draft template, " + count(len(tpl.Items), "exercise", "exercises")
	card.AppliedTitle = "Saved the " + tpl.Name + " template"
	if in.TemplateID != "" {
		card.Subtitle = "Your template, " + count(len(tpl.Items), "exercise", "exercises") + " after this change"
	}
	id, err := t.emitCard(ctx, card)
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "template_id": tpl.Id, "shown": "The user sees the draft and saves or edits it. Nothing is saved yet."}, nil
}
