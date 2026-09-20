package ontology

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestVerifyPassesAfterMutations(t *testing.T) {
	s := NewStore("color", "size")
	fillColors(s, 500)
	if err := s.Verify(); err != nil {
		t.Fatalf("after inserts: %v", err)
	}
	for i := 0; i < 500; i += 2 {
		s.Upsert(fmt.Sprintf("e%05d", i), map[string]any{"color": "black", "size": 99})
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("after updates: %v", err)
	}
	for i := 0; i < 500; i += 3 {
		s.Delete(fmt.Sprintf("e%05d", i))
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("after deletes: %v", err)
	}
}

func TestVerifyDetectsPhantomEntry(t *testing.T) {
	s := NewStore("color")
	fillColors(s, 100)

	s.mu.Lock()
	setAdd(s.index["color"], keyOfMust("red"), "phantom")
	s.mu.Unlock()

	err := s.Verify()
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VerifyError, got %v", err)
	}
	if ve.Attr != "color" || len(ve.Extra) != 1 || ve.Extra[0] != "phantom" {
		t.Fatalf("bad VerifyError: %+v", ve)
	}
	if !strings.Contains(err.Error(), "color") || !strings.Contains(err.Error(), "phantom") {
		t.Fatalf("error lacks location info: %v", err)
	}
}

func TestVerifyDetectsMissingEntry(t *testing.T) {
	s := NewStore("color")
	fillColors(s, 100)

	s.mu.Lock()
	setRemove(s.index["color"], keyOfMust("green"), "e00001")
	s.mu.Unlock()

	err := s.Verify()
	var ve *VerifyError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *VerifyError, got %v", err)
	}
	if len(ve.Missing) != 1 || ve.Missing[0] != "e00001" {
		t.Fatalf("bad VerifyError: %+v", ve)
	}
}

func keyOfMust(v any) string {
	k, ok := keyOf(v)
	if !ok {
		panic("nil key")
	}
	return k
}
