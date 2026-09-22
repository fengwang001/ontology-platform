package message_test

import (
	"ontology/message"
	"ontology/wire"
)

// Test schemas:
//
//	Person: 1 varint id, 2 bytes name, 3 repeated varint tag,
//	        4 message address, 5 message outer
//	Address: 1 varint zip, 2 bytes city
//	Outer:   1 message inner
//	Inner:   1 varint depth
var (
	innerSchema = message.NewSchema(
		message.Field{Number: 1, Kind: message.KindVarint},
	)
	outerSchema = message.NewSchema(
		message.Field{Number: 1, Kind: message.KindMessage, Schema: innerSchema},
	)
	addressSchema = message.NewSchema(
		message.Field{Number: 1, Kind: message.KindVarint},
		message.Field{Number: 2, Kind: message.KindBytes},
	)
	personSchema = message.NewSchema(
		message.Field{Number: 1, Kind: message.KindVarint},
		message.Field{Number: 2, Kind: message.KindBytes},
		message.Field{Number: 3, Kind: message.KindVarint, Repeated: true},
		message.Field{Number: 4, Kind: message.KindMessage, Schema: addressSchema},
		message.Field{Number: 5, Kind: message.KindMessage, Schema: outerSchema},
	)
)

// encField builds one raw field.
func encField(num uint64, t byte, payload []byte) []byte {
	b := wire.AppendVarint(nil, num)
	b = append(b, t)
	if t == wire.Bytes || t == wire.Message {
		b = wire.AppendVarint(b, uint64(len(payload)))
	}
	return append(b, payload...)
}

func encVarintField(num, v uint64) []byte {
	return encField(num, wire.Varint, wire.AppendVarint(nil, v))
}

func encBytesField(num uint64, v []byte) []byte {
	return encField(num, wire.Bytes, v)
}

func encMessageField(num uint64, body []byte) []byte {
	return encField(num, wire.Message, body)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
