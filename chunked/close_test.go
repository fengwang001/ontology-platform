package chunked

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestCloseStates(t *testing.T) {
	cases := []struct {
		name  string
		msg   string
		sent  error
		state State
	}{
		{"in size line", "2", ErrIncomplete, StateSizeLine},
		{"in chunk data", "2\r\na", ErrIncomplete, StateData},
		{"awaiting CRLF", "2\r\nab", ErrIncomplete, StateAwaitCRLF},
		{"half CRLF after data", "2\r\nab\r", ErrHalfCRLF, StateAwaitCRLF},
		{"half CRLF in size line", "2\r", ErrHalfCRLF, StateSizeLine},
		{"awaiting trailer end", "2\r\nab\r\n0\r\nX: 1\r\n", ErrIncomplete, StateTrailer},
		{"half CRLF in trailer", "0\r\nX: 1\r", ErrHalfCRLF, StateTrailer},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := New(Config{})
			if _, err := d.Write([]byte(c.msg)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			err := d.Close()
			if !isErr(err, c.sent) {
				t.Fatalf("Close err=%v, want %v", err, c.sent)
			}
			var cerr *Error
			if !errors.As(err, &cerr) {
				t.Fatalf("err is %T, want *Error", err)
			}
			if cerr.State != c.state {
				t.Errorf("state=%v, want %v", cerr.State, c.state)
			}
			// The four plain states stay distinguishable from half-CRLF.
			if c.sent == ErrIncomplete && isErr(err, ErrHalfCRLF) {
				t.Error("plain incomplete must not match ErrHalfCRLF")
			}
		})
	}
}

func TestCloseStatesDistinct(t *testing.T) {
	states := map[State]bool{}
	for _, msg := range []string{"2", "2\r\na", "2\r\nab", "0\r\nX: 1\r\n"} {
		d := New(Config{})
		if _, err := d.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		var cerr *Error
		if !errors.As(d.Close(), &cerr) {
			t.Fatal("want *Error")
		}
		if states[cerr.State] {
			t.Fatalf("duplicate close state %v", cerr.State)
		}
		states[cerr.State] = true
	}
	if len(states) != 4 {
		t.Fatalf("got %d distinct states, want 4", len(states))
	}
}

func TestCloseComplete(t *testing.T) {
	d := decode(t, []byte(testMessage))
	if err := d.Close(); err != nil {
		t.Fatalf("Close after complete message: %v", err)
	}
}

func TestConcurrentDecodersIsolated(t *testing.T) {
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte('A' + i%26)}, 100+i)
			msg := []byte(fmt.Sprintf("%x\r\n%s\r\n0\r\nT-%d: v\r\n\r\n", len(payload), payload, i))
			d := New(Config{})
			// Feed byte-by-byte to stress resumption per instance.
			for j := 0; j < len(msg); j++ {
				if _, err := d.Write(msg[j : j+1]); err != nil {
					errs <- fmt.Errorf("instance %d: %w", i, err)
					return
				}
			}
			if !d.Done() {
				errs <- fmt.Errorf("instance %d: not done", i)
				return
			}
			if !bytes.Equal(d.Body(), payload) {
				errs <- fmt.Errorf("instance %d: body mismatch", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
