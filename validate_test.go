package sparse

import (
	"errors"
	"math"
	"testing"
)

func TestNotSortedErrorsHaveLocation(t *testing.T) {
	tests := []struct {
		name string
		vec  string
		pos  int
		a, b Vector
	}{
		{"equal indices left", "left", 2,
			Vector{{0, 1}, {5, 2}, {5, 3}}, Vector{{0, 1}}},
		{"decreasing index right", "right", 1,
			Vector{{0, 1}}, Vector{{9, 1}, {3, 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, fn := range []func(Vector, Vector) error{
				func(a, b Vector) error { _, _, e := Dot(a, b); return e },
				func(a, b Vector) error { _, _, e := Cosine(a, b); return e },
			} {
				var ge *Error
				if err := fn(tt.a, tt.b); !errors.As(err, &ge) {
					t.Fatalf("want *Error, got %v", err)
				} else if ge.Kind != ErrNotSorted ||
					ge.Vector != tt.vec || ge.Position != tt.pos {
					t.Fatalf("got kind=%v vector=%q pos=%d, want %v %q %d",
						ge.Kind, ge.Vector, ge.Position,
						ErrNotSorted, tt.vec, tt.pos)
				}
			}
		})
	}
}

func TestNaNErrorsHaveLocation(t *testing.T) {
	good := Vector{{0, 1}}
	bad := Vector{{0, 1}, {2, math.NaN()}}
	if _, _, err := Dot(bad, good); !isError(err, ErrNaN, "left", 1) {
		t.Fatalf("left NaN: got %v", err)
	}
	if _, _, err := Dot(good, bad); !isError(err, ErrNaN, "right", 1) {
		t.Fatalf("right NaN: got %v", err)
	}
}

func isError(err error, kind Kind, vec string, pos int) bool {
	var ge *Error
	return errors.As(err, &ge) && ge.Kind == kind &&
		ge.Vector == vec && ge.Position == pos
}
