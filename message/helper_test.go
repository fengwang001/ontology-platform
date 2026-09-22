package message_test

import "ontology/wire"

// varintF encodes one varint field.
func varintF(num uint64, v uint64) []byte {
	buf := wire.AppendHeader(nil, num, wire.Varint)
	return wire.AppendVarint(buf, v)
}

// bytesF encodes one bytes field.
func bytesF(num uint64, b []byte) []byte {
	buf := wire.AppendHeader(nil, num, wire.Bytes)
	buf = wire.AppendVarint(buf, uint64(len(b)))
	return append(buf, b...)
}

// msgF encodes one message field.
func msgF(num uint64, payload []byte) []byte {
	buf := wire.AppendHeader(nil, num, wire.Message)
	buf = wire.AppendVarint(buf, uint64(len(payload)))
	return append(buf, payload...)
}

// cat concatenates byte slices.
func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
