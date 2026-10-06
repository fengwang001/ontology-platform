package kvlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// On-disk file names within the data directory:
//
//	NNNNNNNN.log    segment data
//	NNNNNNNN.hint   hint for a sealed segment (optional)
//	NNNNNNNN.sealed zero-length marker; its presence makes the segment sealed
//	NNNNNNNN.merge  plain text: space-separated ids replaced by this segment

func segLogName(id int) string    { return fmt.Sprintf("%08d.log", id) }
func segHintName(id int) string   { return fmt.Sprintf("%08d.hint", id) }
func segSealedName(id int) string { return fmt.Sprintf("%08d.sealed", id) }
func segMergeName(id int) string  { return fmt.Sprintf("%08d.merge", id) }

func parseSegID(name, suffix string) (int, bool) {
	if !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	stem := strings.TrimSuffix(name, suffix)
	if len(stem) != 8 {
		return 0, false
	}
	id, err := strconv.Atoi(stem)
	if err != nil || id < 0 {
		return 0, false
	}
	return id, true
}

// segState is the on-disk view of one segment id.
type segState struct {
	id       int
	size     int64
	sealed   bool
	hasHint  bool
	replaces []int // ids listed in the .merge file
}

func (s *Engine) segPath(id int) string {
	return filepath.Join(s.dir, segLogName(id))
}

// listSegments scans the data directory and returns segment states sorted by
// id ascending.
func listSegments(dir string) ([]*segState, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	byID := map[int]*segState{}
	get := func(id int) *segState {
		st := byID[id]
		if st == nil {
			st = &segState{id: id, replaces: nil}
			byID[id] = st
		}
		return st
	}
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() {
			continue
		}
		if id, ok := parseSegID(name, ".log"); ok {
			info, err := ent.Info()
			if err != nil {
				return nil, err
			}
			get(id).size = info.Size()
			continue
		}
		if id, ok := parseSegID(name, ".sealed"); ok {
			get(id).sealed = true
			continue
		}
		if id, ok := parseSegID(name, ".hint"); ok {
			get(id).hasHint = true
			continue
		}
		if id, ok := parseSegID(name, ".merge"); ok {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return nil, err
			}
			st := get(id)
			for _, f := range strings.Fields(string(raw)) {
				v, err := strconv.Atoi(f)
				if err != nil || v < 0 {
					return nil, newError(KindSegmentCorrupt, "list", id, -1, "bad merge manifest")
				}
				st.replaces = append(st.replaces, v)
			}
			sort.Ints(st.replaces)
		}
	}
	out := make([]*segState, 0, len(byID))
	for _, st := range byID {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out, nil
}

// writeFileAtomic writes data via a temp file followed by an atomic rename.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
