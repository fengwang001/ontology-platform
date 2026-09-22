package message

import (
	"bytes"
)

// Marshal returns the wire encoding of e. It never modifies e, so
// marshalling the same value twice yields identical bytes.
//
// Ordering rule (derived from the byte-equivalence invariant): an
// unmodified message re-emits every field at its original position,
// so known and unknown fields stay interleaved exactly as parsed.
// A modified known field is re-encoded at the position of its first
// original occurrence; a deleted known field leaves the unknown
// fields around it untouched; fields added after parsing are
// appended at the end.
func (e *Envelope) Marshal() []byte {
	return e.AppendTo(nil)
}

// AppendTo appends the wire encoding of e to dst.
func (e *Envelope) AppendTo(dst []byte) []byte {
	st := &emitState{}
	if e.Child != nil {
		st.childBytes = e.Child.Marshal()
		st.childSame = len(e.childOcc) > 0 &&
			bytes.Equal(st.childBytes, e.childOcc[len(e.childOcc)-1].payload)
	}
	for _, r := range e.order {
		dst = e.emitRef(dst, r, st)
	}
	return e.emitTrailing(dst, st)
}

// emitState tracks, per field, how many original occurrences have
// been replayed and whether the current value was already emitted.
type emitState struct {
	idIdx, nameIdx, tagIdx, childIdx, unkIdx int
	idDone, nameDone, tagsDone, childDone    bool
	childBytes                               []byte
	childSame                                bool
}

func (e *Envelope) emitRef(dst []byte, r ref, st *emitState) []byte {
	if !r.known {
		if st.unkIdx < e.Unknown.Len() {
			dst = append(dst, e.Unknown.At(st.unkIdx).Raw...)
			st.unkIdx++
		}
		return dst
	}
	switch r.field {
	case fieldID:
		dst = e.emitID(dst, st)
	case fieldName:
		dst = e.emitName(dst, st)
	case fieldTags:
		dst = e.emitTags(dst, st)
	case fieldChild:
		dst = e.emitChild(dst, st)
	}
	return dst
}

func (e *Envelope) emitID(dst []byte, st *emitState) []byte {
	if !e.hasID || st.idDone {
		return dst
	}
	if e.ID == e.origID && st.idIdx < len(e.idOcc) {
		dst = append(dst, e.idOcc[st.idIdx].raw...)
		st.idIdx++
		return dst
	}
	st.idDone = true
	return appendVarintField(dst, fieldID, e.ID)
}

func (e *Envelope) emitName(dst []byte, st *emitState) []byte {
	if !e.hasName || st.nameDone {
		return dst
	}
	if bytes.Equal(e.Name, e.origName) && st.nameIdx < len(e.nameOcc) {
		dst = append(dst, e.nameOcc[st.nameIdx].raw...)
		st.nameIdx++
		return dst
	}
	st.nameDone = true
	return appendBytesField(dst, fieldName, e.Name)
}

func (e *Envelope) emitTags(dst []byte, st *emitState) []byte {
	if tagsEqual(e.Tags, e.origTags) {
		if st.tagIdx < len(e.tagOcc) {
			dst = append(dst, e.tagOcc[st.tagIdx].raw...)
			st.tagIdx++
		}
		return dst
	}
	if st.tagsDone {
		return dst
	}
	st.tagsDone = true
	for _, t := range e.Tags {
		dst = appendBytesField(dst, fieldTags, t)
	}
	return dst
}

func (e *Envelope) emitChild(dst []byte, st *emitState) []byte {
	if e.Child == nil || st.childDone {
		return dst
	}
	if st.childSame && st.childIdx < len(e.childOcc) {
		dst = append(dst, e.childOcc[st.childIdx].raw...)
		st.childIdx++
		return dst
	}
	st.childDone = true
	return appendMessageField(dst, fieldChild, st.childBytes)
}
