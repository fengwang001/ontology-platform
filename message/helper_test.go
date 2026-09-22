package message

import (
	"ontology/wire"
	"testing"
)

// testSchema: field 1 varint, field 2 bytes, field 3 nested message
// (recursively the same schema).
var testSchema = Schema{1: wire.Varint, 2: wire.Bytes, 3: wire.Message}

func newParser(t *testing.T, opts Options) *Parser {
	t.Helper()
	return NewParser(testSchema, opts)
}

func vfield(num uint64, v uint64) []byte {
	return wire.AppendVarintField(nil, num, v)
}

func bfield(num uint64, b []byte) []byte {
	return wire.AppendField(nil, num, wire.Bytes, b)
}

func mfield(num uint64, payload []byte) []byte {
	return wire.AppendField(nil, num, wire.Message, payload)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func mustParse(t *testing.T, p *Parser, data []byte) *Message {
	t.Helper()
	m, err := p.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m == nil {
		t.Fatal("Parse returned nil message with nil error")
	}
	return m
}
