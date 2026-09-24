// Package sink 把聚合快照确定性地落盘：tmp+原子改名，附 CRC32。
package sink

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"time"

	"ontology/internal/agg"
	"ontology/internal/stage"
)

// ErrImmediate 在 sink 配置为立即报错时返回。
var ErrImmediate = errors.New("sink: immediate failure")

// ErrCrashTmp 在注入"写完 tmp 未改名"崩溃时返回。
var ErrCrashTmp = errors.New("sink: injected crash before rename")

// ErrBadOutput 在输出文件校验和不匹配时返回。
var ErrBadOutput = errors.New("sink: output checksum mismatch")

// Config 配置 sink。
type Config struct {
	Dir       string
	PerRecord time.Duration // 每条屏障落盘前的人为延迟
	FailNow   bool          // 第一个静止点立即报错
	CrashTmp  bool          // 只写 tmp 不 rename 即报错
}

// Sink 是落盘阶段：输入为聚合快照信封。
type Sink struct {
	cfg Config
}

// New 构造 sink，并清理目录中残留的未完成 tmp。
func New(cfg Config) (*Sink, error) {
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, err
	}
	if err := CleanLeftovers(cfg.Dir); err != nil {
		return nil, err
	}
	return &Sink{cfg: cfg}, nil
}

// OutPath 是最终输出文件路径。
func OutPath(dir string) string { return filepath.Join(dir, "output.dat") }

func tmpPath(dir string) string { return filepath.Join(dir, "output.dat.tmp") }

// CleanLeftovers 删除目录下所有 .tmp 残留（未完成输出/检查点）。
func CleanLeftovers(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// Work 是阶段工作函数：只在静止点（屏障）落盘完整快照。
func (s *Sink) Work(in stage.Msg[[]agg.Group], emit func(stage.Msg[struct{}])) error {
	if !in.Barrier {
		return nil
	}
	if s.cfg.FailNow {
		return ErrImmediate
	}
	if s.cfg.PerRecord > 0 {
		time.Sleep(s.cfg.PerRecord)
	}
	data, err := encode(in.V)
	if err != nil {
		return err
	}
	tmp := tmpPath(s.cfg.Dir)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if s.cfg.CrashTmp {
		return ErrCrashTmp
	}
	return os.Rename(tmp, OutPath(s.cfg.Dir))
}

// IsLeftover 判断某文件内容是否为"未完成的临时输出"：
// 完整改名输出含正确 CRC；tmp 内容在任何截断点都不可能通过校验。
func IsLeftover(data []byte) bool {
	if len(data) < 4 {
		return true
	}
	body, sum := data[:len(data)-4], binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return true
	}
	var gs []agg.Group
	return json.Unmarshal(body, &gs) != nil
}

// Read 读取并校验最终输出，返回分组快照。
func Read(dir string) ([]agg.Group, error) {
	data, err := os.ReadFile(OutPath(dir))
	if err != nil {
		return nil, err
	}
	if IsLeftover(data) {
		return nil, ErrBadOutput
	}
	var gs []agg.Group
	if err := json.Unmarshal(data[:len(data)-4], &gs); err != nil {
		return nil, err
	}
	return gs, nil
}

func encode(gs []agg.Group) ([]byte, error) {
	body, err := json.Marshal(gs)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(body)+4)
	copy(out, body)
	binary.LittleEndian.PutUint32(out[len(body):], crc32.ChecksumIEEE(body))
	return out, nil
}
