// Package ckpt 负责检查点的写出与恢复：记录已消费屏障位置与各组中间聚合。
// 始终保留最近两个检查点；当前损坏时回退到上一个完好检查点并标记。
package ckpt

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ontology/stage"
)

// ErrCorrupt 表示检查点字节损坏（校验和不匹配）。
var ErrCorrupt = errors.New("ckpt: checksum mismatch")

// State 是一个检查点的全部内容。
type State struct {
	Bar    int64         // 已消费到的屏障序号
	Bads   int64         // 截至该屏障的坏记录累计数
	Groups []stage.Group // 各组中间聚合（按 Key 有序）
}

// Store 管理目录下的两个检查点文件。
type Store struct {
	dir      string
	latest   string
	previous string
}

// New 在 dir 下管理名为 name 与 name.prev 的两个检查点。
func New(dir, name string) *Store {
	return &Store{dir: dir, latest: name, previous: name + ".prev"}
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

// Save 原子滚动：旧 current 变 prev，新快照变 current。
func (s *Store) Save(st State) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	if data, err := os.ReadFile(s.path(s.latest)); err == nil {
		if err := os.WriteFile(s.path(s.previous), data, 0o644); err != nil {
			return err
		}
	}
	payload := encode(st)
	tmp := s.path(s.latest) + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(s.latest))
}

// Load 优先读 current；损坏则回退 prev。fellBack 为 true 时表示发生过回退。
func (s *Store) Load() (st State, fellBack bool, err error) {
	data, err := os.ReadFile(s.path(s.latest))
	if err == nil {
		if st, perr := decode(data); perr == nil {
			return st, false, nil
		}
		err = ErrCorrupt
	}
	pdata, perr := os.ReadFile(s.path(s.previous))
	if perr != nil {
		if errors.Is(err, ErrCorrupt) {
			return State{}, false, err
		}
		return State{}, false, nil // 两个文件都不存在：全新启动
	}
	st, derr := decode(pdata)
	if derr != nil {
		return State{}, false, derr
	}
	return st, true, nil
}

func encode(st State) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "bar=%d\nbads=%d\n", st.Bar, st.Bads)
	for _, g := range st.Groups {
		fmt.Fprintf(&b, "g\t%s\t%s\n", g.Key, strconv.FormatFloat(g.Sum, 'g', -1, 64))
	}
	body := b.String()
	sum := sha256.Sum256([]byte(body))
	return []byte(body + "sha256:" + hex.EncodeToString(sum[:]) + "\n")
}

func decode(data []byte) (State, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], "sha256:") {
		return State{}, ErrCorrupt
	}
	body := strings.Join(lines[:len(lines)-1], "\n") + "\n"
	want := strings.TrimPrefix(lines[len(lines)-1], "sha256:")
	sum := sha256.Sum256([]byte(body))
	if hex.EncodeToString(sum[:]) != want {
		return State{}, ErrCorrupt
	}
	var st State
	for _, ln := range lines[:len(lines)-1] {
		switch {
		case strings.HasPrefix(ln, "bar="):
			v, _ := strconv.ParseInt(strings.TrimPrefix(ln, "bar="), 10, 64)
			st.Bar = v
		case strings.HasPrefix(ln, "bads="):
			v, _ := strconv.ParseInt(strings.TrimPrefix(ln, "bads="), 10, 64)
			st.Bads = v
		case strings.HasPrefix(ln, "g\t"):
			f := strings.Split(ln, "\t")
			v, _ := strconv.ParseFloat(f[2], 64)
			st.Groups = append(st.Groups, stage.Group{Key: f[1], Sum: v})
		}
	}
	return st, nil
}

// Corrupt 翻转 current 检查点若干字节，模拟写坏（供故障注入）。
func (s *Store) Corrupt() error {
	p := s.path(s.latest)
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if len(data) > 0 {
		data[0] ^= 0xFF
	}
	return os.WriteFile(p, data, 0o644)
}
