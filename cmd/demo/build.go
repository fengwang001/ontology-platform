package main

import "ontology/wire"

var demoSchema = newDemoSchema()

func vfield(num, v uint64) []byte {
	p := wire.AppendVarint(nil, v)
	b := wire.AppendVarint(nil, num)
	b = append(b, wire.Varint)
	return append(b, p...)
}

func bfield(num uint64, v []byte) []byte {
	b := wire.AppendVarint(nil, num)
	b = append(b, wire.Bytes)
	b = wire.AppendVarint(b, uint64(len(v)))
	return append(b, v...)
}

func mfield(num uint64, body []byte) []byte {
	b := wire.AppendVarint(nil, num)
	b = append(b, wire.Message)
	b = wire.AppendVarint(b, uint64(len(body)))
	return append(b, body...)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
