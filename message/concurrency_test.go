package message_test

import (
	"bytes"
	"sync"
	"testing"

	"ontology/message"
)

func TestConcurrentIndependentMessages(t *testing.T) {
	inputs := make([][]byte, 32)
	for i := range inputs {
		inputs[i] = concat(
			encVarintField(1, uint64(i)),
			encVarintField(200, uint64(i*7)),
			encBytesField(201, bytes.Repeat([]byte{byte(i)}, 16)),
		)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(inputs)*2)
	for i := range inputs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m, err := message.Parse(inputs[i], personSchema, defaultLimits)
			if err != nil {
				errs <- err
				return
			}
			a, err := m.Marshal()
			if err != nil {
				errs <- err
				return
			}
			b, err := m.Marshal()
			if err != nil || !bytes.Equal(a, b) || !bytes.Equal(a, inputs[i]) {
				errs <- errMismatch{i, a, inputs[i]}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type errMismatch struct {
	idx  int
	got  []byte
	want []byte
}

func (e errMismatch) Error() string {
	return "round trip mismatch in goroutine"
}
