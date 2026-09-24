package event

import "testing"

func TestEventRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
	}{
		{"zero seq empty payload", Event{0, nil}},
		{"single event empty payload", Event{1, []byte{}}},
		{"typical payload", Event{42, []byte("hello world")}},
		{"binary payload", Event{1<<63 - 1, []byte{0, 1, 2, 255, 0, 9}}},
		{"large seq", Event{1<<64 - 1, []byte("max")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, tc.ev.EncodedLen())
			if err := tc.ev.Encode(buf); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := Decode(buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Seq != tc.ev.Seq {
				t.Fatalf("seq: got %d want %d", got.Seq, tc.ev.Seq)
			}
			wantLen := len(tc.ev.Payload)
			if len(got.Payload) != wantLen || string(got.Payload) != string(tc.ev.Payload) {
				t.Fatalf("payload: got %q want %q", got.Payload, tc.ev.Payload)
			}
			if got.EncodedLen() != len(buf) {
				t.Fatalf("encoded len: got %d want %d", got.EncodedLen(), len(buf))
			}
			raw := tc.ev.AppendEncode(nil)
			if string(raw) != string(buf) {
				t.Fatalf("AppendEncode mismatch: %x vs %x", raw, buf)
			}
		})
	}
}

func TestEventErrorsAndCopy(t *testing.T) {
	ev := Event{Seq: 7, Payload: []byte("abc")}
	if err := ev.Encode(make([]byte, ev.EncodedLen()-1)); err != ErrShortBuffer {
		t.Fatalf("short dst: want ErrShortBuffer, got %v", err)
	}
	for _, n := range []int{0, 1, 7} {
		if _, err := Decode(make([]byte, n)); err != ErrShortBuffer {
			t.Fatalf("decode %d bytes: want ErrShortBuffer, got %v", n, err)
		}
	}
	buf := make([]byte, ev.EncodedLen())
	if err := ev.Encode(buf); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(buf)
	if err != nil {
		t.Fatal(err)
	}
	buf[8] = 'X' // 改写源缓冲不得影响已解码事件
	if string(got.Payload) != "abc" {
		t.Fatalf("decode did not copy payload: %q", got.Payload)
	}
}
