package ontology

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAuditLogRecordsInputOutputAndBasis(t *testing.T) {
	al, de := allowDeny()
	h := newHarness(t, Config{
		RowMode: DenyOverrides, PropertyMode: DenyOverrides,
		DefaultRow: EffectAllow, DefaultRead: EffectAllow, DefaultWrite: EffectDeny,
		WriteMode: WriteReject,
	})
	h.store.Put(Instance{
		Type:    empType,
		ID:      "a1",
		Values:  map[string]Value{"name": {Str: "A"}, "salary": {Int: 1}},
		Present: map[string]bool{"name": true, "salary": true},
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "pw", ObjectType: empType, Property: "name", Write: al,
	})
	h.catalog.RegisterPropertyPolicy(PropertyPolicy{
		ID: "pd", ObjectType: empType, Property: "salary", Write: de,
	})

	_, err := h.a.Write("s", empType, "a1", map[string]Value{
		"salary": {Int: 5}, "name": {Str: "B"},
	})
	if err == nil || err.Kind != ErrPropertyNotWritable {
		t.Fatalf("want reject, got %v", err)
	}
	if _, err := h.a.Read("s", empType, "a1"); err != nil {
		t.Fatal(err)
	}

	// Decode from a copy: json.Decoder advances the bytes.Buffer read offset,
	// and the line-count assertion below must see the whole stream.
	raw := append([]byte(nil), h.buf.Bytes()...)
	dec := json.NewDecoder(bytes.NewReader(raw))
	var writes, reads int
	for {
		var entry AuditEntry
		if err := dec.Decode(&entry); err != nil {
			break
		}
		if entry.Op == "write" {
			writes++
			if entry.Input == nil {
				t.Fatal("write entry must record input")
			}
			if entry.Error == "" {
				t.Fatal("rejected write entry must record the error")
			}
			if entry.Basis == nil || len(entry.Basis.MatchedProperty) == 0 {
				t.Fatal("write entry must record property policy basis")
			}
			if entry.Basis.Touched <= 0 {
				t.Fatal("basis must record touched policy count")
			}
		}
		if entry.Op == "read" {
			reads++
			if entry.Output == nil {
				t.Fatal("read entry must record output")
			}
		}
	}
	if writes != 1 || reads != 1 {
		t.Fatalf("writes=%d reads=%d", writes, reads)
	}

	// JSONL must be independently parseable and each line must stand alone.
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("want 2 JSONL lines, got %d", len(lines))
	}
}
