package ontology

import (
	"errors"
	"testing"
)

func TestValidateOK(t *testing.T) {
	cases := [][]Entry{
		nil,
		{},
		{{Key: "a", Value: "1"}},
		{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}, {Key: "c", Value: ""}},
	}
	for i, snap := range cases {
		if err := Validate(snap); err != nil {
			t.Fatalf("case %d: expected valid, got %v", i, err)
		}
	}
}

func TestValidateFirstViolation(t *testing.T) {
	tests := []struct {
		name string
		snap []Entry
		want error
	}{
		{"empty key", []Entry{{Key: "a"}, {Key: ""}}, ErrEmptyKey},
		{"duplicate key", []Entry{{Key: "a"}, {Key: "a"}}, ErrDuplicateKey},
		{"unsorted", []Entry{{Key: "b"}, {Key: "a"}}, ErrUnsorted},
		{"first violation wins: dup before later unsorted",
			[]Entry{{Key: "a"}, {Key: "a"}, {Key: "0"}}, ErrDuplicateKey},
		{"first violation wins: unsorted before later dup",
			[]Entry{{Key: "b"}, {Key: "a"}, {Key: "a"}}, ErrUnsorted},
		{"empty key before duplicate",
			[]Entry{{Key: ""}, {Key: ""}}, ErrEmptyKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.snap)
			if !errors.Is(err, tt.want) {
				t.Fatalf("want %v, got %v", tt.want, err)
			}
			var de *Error
			if !errors.As(err, &de) {
				t.Fatalf("expected *Error, got %T", err)
			}
		})
	}
}

func TestErrorCategoriesAreDistinct(t *testing.T) {
	sentinels := []error{ErrInvalidConfig, ErrEmptyKey, ErrDuplicateKey, ErrUnsorted, ErrTooManyChanges}
	for i := range sentinels {
		for j := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(sentinels[i], sentinels[j]) {
				t.Fatalf("sentinels %v and %v are not distinguishable", sentinels[i], sentinels[j])
			}
		}
	}
	kinds := map[ErrorKind]bool{
		KindInvalidConfig:  true,
		KindEmptyKey:       true,
		KindDuplicateKey:   true,
		KindUnsorted:       true,
		KindTooManyChanges: true,
	}
	if len(kinds) != 5 {
		t.Fatalf("error kinds must be mutually distinct, got %v", kinds)
	}
}
