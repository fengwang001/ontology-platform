package twophase

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FileStore 用目录实现 Store：
//   - effects/NNN.effect 单条副作用，先写临时文件并 fsync，再原子 rename，再 fsync 目录；
//   - position.txt 记录已提交位点，temp+rename 原子替换，读到的永远是完整的旧值或新值。
type FileStore struct {
	dir string
}

func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "effects"), 0o755); err != nil {
		return nil, fmt.Errorf("twophase: mkdir store: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) effectPath(seq int64) string {
	return filepath.Join(s.dir, "effects", fmt.Sprintf("%020d.effect", seq))
}

func (s *FileStore) positionPath() string {
	return filepath.Join(s.dir, "position.txt")
}

func fsyncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *FileStore) LoadPosition() (int64, error) {
	f, err := os.Open(s.positionPath())
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return 0, fmt.Errorf("twophase: read position: %w", err)
	}
	pos, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("twophase: parse position %q: %w", line, err)
	}
	if pos < 0 {
		return 0, fmt.Errorf("twophase: negative position %d", pos)
	}
	return pos, nil
}

func (s *FileStore) LoadEffect(seq int64) (*Effect, error) {
	data, err := os.ReadFile(s.effectPath(seq))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	action := strings.TrimSuffix(string(data), "\n")
	return &Effect{Seq: seq, Action: action}, nil
}

func (s *FileStore) SaveEffect(effect Effect) error {
	if effect.Seq <= 0 {
		return fmt.Errorf("twophase: invalid effect seq %d", effect.Seq)
	}
	final := s.effectPath(effect.Seq)
	if _, err := os.Stat(final); err == nil {
		// 幂等：已持久化则不重复写，冲突由提交器层比对判定。
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(effect.Action + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return fsyncDir(filepath.Join(s.dir, "effects"))
}

func (s *FileStore) SavePosition(position int64) error {
	if position < 0 {
		return fmt.Errorf("twophase: negative position %d", position)
	}
	final := s.positionPath()
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%d\n", position); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return fsyncDir(s.dir)
}

func (s *FileStore) ListPending(position int64) ([]int64, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "effects"))
	if err != nil {
		return nil, err
	}
	var pending []int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".effect") || entry.IsDir() {
			continue
		}
		seq, err := strconv.ParseInt(strings.TrimSuffix(name, ".effect"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("twophase: bad effect file %q: %w", name, err)
		}
		if seq > position {
			pending = append(pending, seq)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	return pending, nil
}
