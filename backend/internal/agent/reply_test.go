package agent

import (
	"strings"
	"testing"
)

// However the model's words are cut into pieces on the way in, the answer
// arrives whole and the suggestions line never does.
func TestReplyHoldsBackSuggestionsHoweverItIsSplit(t *testing.T) {
	t.Parallel()
	const answer, line = "Bench is up > last month.\n\nChest press dipped.", "\n\n>> Chart my bench press | Start a pull day"
	text := answer + line
	for a := 0; a <= len(text); a++ {
		for b := a; b <= len(text); b++ {
			var said strings.Builder
			r := reply{say: func(s string) { said.WriteString(s) }}
			for _, piece := range []string{text[:a], text[a:b], text[b:]} {
				r.write(piece)
			}
			suggestions := r.finish()
			if said.String() != answer || len(suggestions) != 2 || suggestions[1].GetLabel() != "Start a pull day" {
				t.Fatalf("split at %d and %d: said %q, %d suggestions", a, b, said.String(), len(suggestions))
			}
		}
	}
}
