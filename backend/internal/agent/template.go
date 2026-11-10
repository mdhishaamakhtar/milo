package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

type templateInput struct {
	Name  string              `json:"name"`
	Notes string              `json:"notes,omitempty"`
	Items []templateItemInput `json:"items"`
}

type templateItemInput struct {
	ExerciseID string `json:"exercise_id"`
	GroupLabel string `json:"group_label,omitempty" jsonschema_description:"Supersets share a label: 4a and 4b both have \"4\"."`
	Note       string `json:"note,omitempty" jsonschema_description:"A word or two for the card, e.g. \"fresh\", \"volume\"."`
}

// proposeTemplate drafts a template: exercises in order, supersets grouped, no numbers.
func (t *turn) proposeTemplate(ctx context.Context, in templateInput) (any, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(in.Items) == 0 {
		return nil, badInput("a template needs a name and at least one exercise")
	}
	tpl := &milo.Template{Id: newID().String(), Name: name, Notes: in.Notes, UpdatedAt: ts(t.now)}
	groupSize := map[string]int{}
	for _, it := range in.Items {
		groupSize[it.GroupLabel]++
	}
	lines := make([]*milo.CardLine, len(in.Items))
	number, seen := 0, map[string]int{}
	for i, it := range in.Items {
		id, err := parseUUID(it.ExerciseID, "exercise_id")
		if err != nil {
			return nil, err
		}
		ex, err := t.visibleExercise(ctx, id)
		if err != nil {
			return nil, err
		}
		tpl.Items = append(tpl.Items, &milo.TemplateItem{ExerciseId: id.String(), GroupLabel: it.GroupLabel})
		// Card labels: 1, 2, 3, 4a, 4b.
		label := ""
		if it.GroupLabel != "" && groupSize[it.GroupLabel] > 1 {
			if seen[it.GroupLabel] == 0 {
				number++
			}
			label = fmt.Sprintf("%d%c", number, 'a'+seen[it.GroupLabel])
			seen[it.GroupLabel]++
		} else {
			number++
			label = fmt.Sprint(number)
		}
		lines[i] = &milo.CardLine{Label: label, After: ex.Name, Before: it.Note}
	}
	id, err := t.emitCard(ctx, &milo.Card{
		Kind: milo.CardKind_CARD_KIND_TEMPLATE_DRAFT, Title: name,
		Subtitle: fmt.Sprintf("Draft template, %d %s", len(tpl.Items), plural(len(tpl.Items), "exercise", "exercises")),
		Lines:    lines, Changes: []*milo.Change{rows.Change(tpl)},
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"card_id": id, "shown": "The user sees the draft and saves or edits it."}, nil
}
