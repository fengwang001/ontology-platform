package message

import (
	"bytes"

	"ontology/wire"
)

// emitTrailing appends everything not covered by the recorded order:
// fields set after parsing, and unknown fields appended by the caller
// beyond the ones the parser recorded.
func (e *Envelope) emitTrailing(dst []byte, st *emitState) []byte {
	if e.hasID && !st.idDone && st.idIdx == 0 {
		dst = appendVarintField(dst, fieldID, e.ID)
	}
	if e.hasName && !st.nameDone && st.nameIdx == 0 {
		dst = appendBytesField(dst, fieldName, e.Name)
	}
	if !st.tagsDone && st.tagIdx == 0 {
		for _, t := range e.Tags {
			dst = appendBytesField(dst, fieldTags, t)
		}
	}
	if e.Child != nil && !st.childDone && st.childIdx == 0 {
		dst = appendMessageField(dst, fieldChild, st.childBytes)
	}
	for st.unkIdx < e.Unknown.Len() {
		dst = append(dst, e.Unknown.At(st.unkIdx).Raw...)
		st.unkIdx++
	}
	return dst
}

func tagsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func appendVarintField(dst []byte, num uint64, v uint64) []byte {
	dst = wire.AppendHeader(dst, num, wire.Varint)
	return wire.AppendVarint(dst, v)
}

func appendBytesField(dst []byte, num uint64, b []byte) []byte {
	dst = wire.AppendHeader(dst, num, wire.Bytes)
	dst = wire.AppendVarint(dst, uint64(len(b)))
	return append(dst, b...)
}

func appendMessageField(dst []byte, num uint64, payload []byte) []byte {
	dst = wire.AppendHeader(dst, num, wire.Message)
	dst = wire.AppendVarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}
