// Package sink 把聚合快照落盘：先写临时文件、附校验和、再原子改名。
package sink

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ontology/stage"
)

// ErrTemp 表示发现了未完成的临时文件或校验和不匹配的输出。
var ErrTemp = errors.New("sink: incomplete temp file or checksum mismatch")

const tmpSuffix = ".tmp"

// Config 配置落盘。
type Config struct {
	Dir          string
	Name         string        // 最终输出文件名
	Delay        time.Duration // 每次快照落盘前的延迟，用于拖慢下游
	Fail         bool          // 第一次 Write 立即返回错误
	BeforeRename func() bool   // 临时文件写好、改名前钩子：返回 true 模拟崩溃（不再改名）
}

// Sink 是落盘器。输出是确定性的全量快照，因此恢复后下一个快照会完全覆盖旧内容。
type Sink struct {
	cfg  Config
	fail bool
}

// New 创建落盘器并清理目录里的残留临时文件。
func New(cfg Config) (*Sink, error) {
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := CleanTemp(cfg.Dir); err != nil {
		return nil, err
	}
	return &Sink{cfg: cfg}, nil
}

// Path 返回最终输出文件的完整路径。
func (s *Sink) Path() string { return filepath.Join(s.cfg.Dir, s.cfg.Name) }

// Write 把快照原子落盘，返回临时文件完整字节（便于测试截断）。
func (s *Sink) Write(snap stage.Snapshot) ([]byte, error) {
	if s.cfg.Fail && !s.fail {
		s.fail = true
		return nil, errors.New("sink: injected immediate failure")
	}
	if s.cfg.Delay > 0 {
		time.Sleep(s.cfg.Delay)
	}
	payload := encode(snap)
	tmp := s.Path() + tmpSuffix
	if err := os.WriteFile(tmp, payload, 0o644); err != nil {
		return nil, err
	}
	if s.cfg.BeforeRename != nil && s.cfg.BeforeRename() {
		return payload, nil // 模拟硬崩溃：临时文件残留，绝不改名
	}
	if err := os.Rename(tmp, s.Path()); err != nil {
		return nil, err
	}
	return payload, nil
}

func encode(snap stage.Snapshot) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "bar=%d bads=%d\n", snap.Bar, snap.Bads)
	for _, g := range snap.Groups {
		b.WriteString(g.Key)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatFloat(g.Sum, 'g', -1, 64))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256(b.Bytes())
	fmt.Fprintf(&b, "sha256:%s\n", hex.EncodeToString(sum[:]))
	return b.Bytes()
}

// IsTemp 按命名后缀判定是否为未完成的临时文件。
func IsTemp(path string) bool { return strings.HasSuffix(path, tmpSuffix) }

// ValidFinal 校验最终输出字节的校验和；任何截断都会失败。
func ValidFinal(data []byte) bool {
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) < 2 || !bytes.HasPrefix(lines[len(lines)-2], []byte("sha256:")) {
		return false
	}
	body := data[:len(data)-len(lines[len(lines)-2])-1]
	want := strings.TrimPrefix(string(lines[len(lines)-2]), "sha256:")
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]) == want
}

// CleanTemp 删除目录下所有 .tmp 残留；最终文件若校验和损坏也按未完成处理。
func CleanTemp(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if IsTemp(e.Name()) {
			if err := os.Remove(full); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(full)
		if err == nil && !ValidFinal(data) {
			if err := os.Remove(full); err != nil {
				return err
			}
		}
	}
	return nil
}
