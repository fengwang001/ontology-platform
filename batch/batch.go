// Package batch defines an import batch (ID + ordered business keys)
// and the encoding/decoding of batch manifests.
package batch

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Batch is an ordered list of business keys under one batch ID.
type Batch struct {
	ID   string
	Keys []string
}

// DupError reports a business key appearing twice in one batch.
type DupError struct {
	Key    string
	First  int
	Second int
}

func (e *DupError) Error() string {
	return fmt.Sprintf("batch: duplicate key %q at positions %d and %d", e.Key, e.First, e.Second)
}

// New validates id and keys and returns the batch. Empty keys are legal;
// an empty ID or a duplicated key rejects the whole batch.
func New(id string, keys []string) (*Batch, error) {
	if id == "" {
		return nil, errors.New("batch: empty batch ID")
	}
	if strings.ContainsAny(id, " \t\n") {
		return nil, errors.New("batch: batch ID must not contain whitespace")
	}
	seen := make(map[string]int, len(keys))
	for i, k := range keys {
		if j, ok := seen[k]; ok {
			return nil, &DupError{Key: k, First: j, Second: i}
		}
		seen[k] = i
	}
	return &Batch{ID: id, Keys: keys}, nil
}

// Len returns the record count.
func (b *Batch) Len() int { return len(b.Keys) }

// RecordValue is the deterministic stored value for a record of a batch.
func RecordValue(id, key string) string { return id + "\x00" + key }

// Encode writes the manifest: a header line then one quoted key per line.
func Encode(w io.Writer, b *Batch) error {
	if _, err := fmt.Fprintf(w, "B %s %d\n", b.ID, len(b.Keys)); err != nil {
		return err
	}
	for _, k := range b.Keys {
		if _, err := fmt.Fprintf(w, "K %s\n", strconv.Quote(k)); err != nil {
			return err
		}
	}
	return nil
}

// Decode reads a manifest written by Encode and validates it via New.
func Decode(r io.Reader) (*Batch, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if !sc.Scan() {
		return nil, errors.New("batch: empty manifest")
	}
	var id string
	var n int
	if _, err := fmt.Sscanf(sc.Text(), "B %s %d", &id, &n); err != nil {
		return nil, fmt.Errorf("batch: bad manifest header: %w", err)
	}
	keys := make([]string, 0, n)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "K ") {
			return nil, fmt.Errorf("batch: bad manifest line %q", line)
		}
		k, err := strconv.Unquote(line[2:])
		if err != nil {
			return nil, fmt.Errorf("batch: bad key encoding: %w", err)
		}
		keys = append(keys, k)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(keys) != n {
		return nil, fmt.Errorf("batch: manifest declares %d records, got %d", n, len(keys))
	}
	return New(id, keys)
}
