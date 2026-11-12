package rows_test

import (
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/mdhishaamakhtar/milo/backend/gen/milo"
	"github.com/mdhishaamakhtar/milo/backend/internal/rows"
)

func TestEnumNames(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		enum protoreflect.Enum
		name string
	}{
		{milo.Measurement_MEASUREMENT_REPS_LOAD, "reps_load"},
		{milo.Measurement_MEASUREMENT_ASSISTED, "assisted"},
		{milo.SetKind_SET_KIND_WARMUP, "warmup"},
		{milo.WeightUnit_WEIGHT_UNIT_LB, "lb"},
		{milo.CardStatus_CARD_STATUS_UNDONE, "undone"},
		{milo.Measurement_MEASUREMENT_UNSPECIFIED, ""},
	} {
		if got := rows.Name(c.enum); got != c.name {
			t.Errorf("Name(%v) = %q, want %q", c.enum, got, c.name)
		}
	}
	if got := rows.Parse[milo.SetKind]("warmup"); got != milo.SetKind_SET_KIND_WARMUP {
		t.Errorf("Parse(warmup) = %v", got)
	}
	if got := rows.Parse[milo.WeightUnit]("stone"); got != milo.WeightUnit_WEIGHT_UNIT_UNSPECIFIED {
		t.Errorf("Parse(stone) = %v, want unspecified", got)
	}
}

func TestValidWeight(t *testing.T) {
	t.Parallel()
	for w, ok := range map[string]bool{"": true, "45": true, "20.41": true, "15.875": true, "1.2345": false, "forty": false, "-5": false, "1234567": false} {
		if rows.ValidWeight(w) != ok {
			t.Errorf("ValidWeight(%q) = %v, want %v", w, !ok, ok)
		}
	}
}
