package message_test

import (
	"bytes"
	"testing"
)

// editInput is: id=1, unk9("A"), name="n", unk9("B"), id=2.
func editInput() []byte {
	return cat(
		vid(1, 1),
		vbytes(9, []byte("A")),
		vbytes(2, []byte("n")),
		vbytes(9, []byte("B")),
		vid(1, 2),
	)
}

func TestSetIDKeepsUnknownsInPlace(t *testing.T) {
	m := mustParse(t, editInput())
	m.SetID(99)
	want := cat(
		vid(1, 99),             // replaced at first occurrence's slot
		vbytes(9, []byte("A")), // unknown untouched
		vbytes(2, []byte("n")),
		vbytes(9, []byte("B")), // unknown untouched
		// second id occurrence removed
	)
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("after SetID:\n got %x\nwant %x", got, want)
	}
}

func TestSetNameKeepsUnknownsInPlace(t *testing.T) {
	m := mustParse(t, editInput())
	m.SetName([]byte("a-longer-name"))
	want := cat(
		vid(1, 1),
		vbytes(9, []byte("A")),
		vbytes(2, []byte("a-longer-name")),
		vbytes(9, []byte("B")),
		vid(1, 2),
	)
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("after SetName:\n got %x\nwant %x", got, want)
	}
}

func TestClearIDKeepsUnknownOrder(t *testing.T) {
	m := mustParse(t, editInput())
	m.ClearID()
	want := cat(
		vbytes(9, []byte("A")),
		vbytes(2, []byte("n")),
		vbytes(9, []byte("B")),
	)
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("after ClearID:\n got %x\nwant %x", got, want)
	}
}

func TestClearNameKeepsUnknownOrder(t *testing.T) {
	m := mustParse(t, editInput())
	m.ClearName()
	want := cat(
		vid(1, 1),
		vbytes(9, []byte("A")),
		vbytes(9, []byte("B")),
		vid(1, 2),
	)
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("after ClearName:\n got %x\nwant %x", got, want)
	}
}

func TestEditNestedChildKeepsUnknowns(t *testing.T) {
	inner := cat(vid(5, 1), vid(1, 7), vbytes(8, []byte("deep")))
	top := cat(vid(1, 1), vmsg(3, inner), vbytes(10, []byte("top")))

	m := mustParse(t, top)
	m.Child().SetID(42)

	wantInner := cat(vid(5, 1), vid(1, 42), vbytes(8, []byte("deep")))
	want := cat(vid(1, 1), vmsg(3, wantInner), vbytes(10, []byte("top")))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("after nested edit:\n got %x\nwant %x", got, want)
	}
}

func TestSetOnEmptyMessageAppends(t *testing.T) {
	m := mustParse(t, vbytes(9, []byte("u")))
	m.SetID(7)
	want := cat(vbytes(9, []byte("u")), vid(1, 7))
	if got := m.Marshal(); !bytes.Equal(got, want) {
		t.Fatalf("SetID on message without id:\n got %x\nwant %x", got, want)
	}
}
