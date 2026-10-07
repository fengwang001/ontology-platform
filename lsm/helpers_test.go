package lsm

import (
	"fmt"
	"testing"
)

// k 把单字节键转成字节串，便于书写测试。
func k(b byte) []byte { return []byte{b} }

// k2 把两字节键转成字节串。
func k2(a, b byte) []byte { return []byte{a, b} }

// mf 构造文件元数据。
func mf(id uint64, level int, lo, hi byte, size int64) FileMeta {
	return FileMeta{ID: id, Level: level, Smallest: k(lo), Largest: k(hi), Size: size}
}

// mustAdd 登记文件，失败即终止测试。
func mustAdd(t *testing.T, s *Service, files ...FileMeta) {
	t.Helper()
	for _, f := range files {
		if err := s.AddFile(f); err != nil {
			t.Fatalf("AddFile(%+v) failed: %v", f, err)
		}
	}
}

// mustPick 选取计划，要求非空。
func mustPick(t *testing.T, s *Service) *Plan {
	t.Helper()
	p, err := s.Pick()
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if p == nil {
		t.Fatalf("Pick returned nil plan")
	}
	return p
}

// ids 提取文件编号序列。
func ids(files []FileMeta) []uint64 {
	out := make([]uint64, len(files))
	for i, f := range files {
		out[i] = f.ID
	}
	return out
}

// eqIDs 比较编号序列。
func eqIDs(got []FileMeta, want ...uint64) error {
	g := ids(got)
	if len(g) != len(want) {
		return fmt.Errorf("got ids %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			return fmt.Errorf("got ids %v, want %v", g, want)
		}
	}
	return nil
}

// testCfg 常用测试配置：4 层，L0 阈值 4，L1 目标 1000 字节，倍数 10。
func testCfg() Config {
	return Config{NumLevels: 4, L0Trigger: 4, BaseLevelBytes: 1000, LevelMultiplier: 10}
}

// newTestService 创建测试服务。
func newTestService(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	return s
}
