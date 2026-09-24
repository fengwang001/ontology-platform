// Package sink atomically persists aggregation output to disk.
//
// Every commit renders the full snapshot to "<name>.tmp.*", fsyncs it and
// renames it over the final file. Only the final file name is ever read as
// valid output; leftover temp files are garbage from a crashed commit.
package sink

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"time"

	"ontology/ckpt"
)

// Sink writes one output file inside a directory.
type Sink struct {
	dir   string
	file  string
	delay time.Duration // artificial per-line delay for backpressure tests
}

// New creates a sink for dir/file. delay sleeps once per rendered group line.
func New(dir, file string, delay time.Duration) *Sink {
	return &Sink{dir: dir, file: file, delay: delay}
}

// Render serialises groups: one sorted "key count=n sum=s" line per group plus
// a final sha256 trailer line over the body.
func Render(groups map[string]ckpt.Agg) []byte {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var body bytes.Buffer
	for _, k := range keys {
		a := groups[k]
		body.WriteString(k)
		body.WriteString(" count=")
		body.WriteString(itoa(a.Count))
		body.WriteString(" sum=")
		body.WriteString(itoa(a.Sum))
		body.WriteByte('\n')
	}
	h := sha256.Sum256(body.Bytes())
	body.WriteString("sha256=")
	body.WriteString(hex.EncodeToString(h[:]))
	body.WriteByte('\n')
	return body.Bytes()
}

// Validate reports whether data is a complete rendered output: the trailing
// line must carry the SHA-256 of the body. Any truncation fails.
func Validate(data []byte) bool {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return false
	}
	last := bytes.LastIndexByte(data[:len(data)-1], '\n')
	if last < 0 {
		return false
	}
	h := sha256.Sum256(data[:last+1])
	want := "sha256=" + hex.EncodeToString(h[:]) + "\n"
	return bytes.Equal(data[last+1:], []byte(want))
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := ""
	if v < 0 {
		neg, v = "-", -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return neg + string(b[i:])
}

// Commit atomically replaces the output file with a rendering of groups.
func (s *Sink) Commit(groups map[string]ckpt.Agg) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if s.delay > 0 {
		time.Sleep(s.delay * time.Duration(max1(len(groups))))
	}
	data := Render(groups)
	tmp, err := os.CreateTemp(s.dir, filepath.Base(s.file)+".tmp.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(s.dir, s.file)); err != nil {
		return err
	}
	ok = true
	return syncDir(s.dir)
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// Read returns the final output bytes after checksum validation.
func (s *Sink) Read() ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, s.file))
	if err != nil {
		return nil, err
	}
	if !Validate(data) {
		return nil, os.ErrInvalid
	}
	return data, nil
}

// CleanTemp removes leftover "<file>.tmp.*" files and returns their names.
func (s *Sink) CleanTemp() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(s.dir, filepath.Base(s.file)+".tmp.*"))
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(matches))
	for _, m := range matches {
		if err := os.Remove(m); err != nil {
			return removed, err
		}
		removed = append(removed, m)
	}
	return removed, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	_ = d.Close()
	return err
}
