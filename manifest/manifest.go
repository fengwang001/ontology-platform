// Package manifest 维护作业清单的连续终态前沿与分版发布。
package manifest

import "errors"

var (
	// ErrNotReady 尚无任何已发布版本。
	ErrNotReady = errors.New("manifest: 未就绪")
	// ErrVersionNotFound 版本号越界。
	ErrVersionNotFound = errors.New("manifest: 版本不存在")
)

// Entry 清单中的一档（Bitrate 为钳制后的值，Size 为 Done 时记录的产出大小）。
type Entry struct {
	Name     string
	Height   int
	Bitrate  int
	Size     int64
	Required bool
}

// Version 一版清单：版本号自 1 起连续，Rungs 按高度升序。
type Version struct {
	Number int
	Rungs  []Entry
}

const (
	stOpen = iota // Pending 或 Running
	stDone
	stFailed
)

// Manifest 单作业的清单状态。不是并发安全的，由上层串行化。
type Manifest struct {
	entries  []Entry // 全阶梯，按高度升序
	state    []int8
	frontier int   // 连续终态前沿，只进不退
	listed   []int // 当前可列集合 L（entries 下标，升序）
	reqTotal int
	reqInL   int
	versions []Version
	advance  int // 前沿指针检查档的次数：总量 <= 档数 + 被接受的 Finish 数
}

// New 按阶梯（高度升序）创建清单状态。
func New(entries []Entry) *Manifest {
	m := &Manifest{
		entries: append([]Entry(nil), entries...),
		state:   make([]int8, len(entries)),
	}
	for _, e := range entries {
		if e.Required {
			m.reqTotal++
		}
	}
	return m
}

// OnTerminal 记录某档到达终态（done 为 true 表示 Done，否则 Failed），
// 随后从上次停止处继续推进前沿：Failed 跳过，Done 列入 L，遇未决档即停。
func (m *Manifest) OnTerminal(rung int, done bool, size int64) {
	if done {
		m.state[rung] = stDone
		m.entries[rung].Size = size
	} else {
		m.state[rung] = stFailed
	}
	for m.frontier < len(m.entries) {
		m.advance++
		switch m.state[m.frontier] {
		case stDone:
			m.listed = append(m.listed, m.frontier)
			if m.entries[m.frontier].Required {
				m.reqInL++
			}
			m.frontier++
		case stFailed:
			m.frontier++
		default:
			return
		}
	}
}

// MaybePublish 若 L 包含全部 required 档且与上一版内容不同（首版与空集
// 比较）则发布新版本。L 只增不减，故长度不同即内容不同。一次调用最多增一版。
func (m *Manifest) MaybePublish() (Version, bool) {
	if m.reqInL < m.reqTotal {
		return Version{}, false
	}
	if len(m.listed) == m.publishedLen() {
		return Version{}, false
	}
	rungs := make([]Entry, len(m.listed))
	for i, idx := range m.listed {
		rungs[i] = m.entries[idx]
	}
	v := Version{Number: len(m.versions) + 1, Rungs: rungs}
	m.versions = append(m.versions, v)
	return v, true
}

func (m *Manifest) publishedLen() int {
	if len(m.versions) == 0 {
		return 0
	}
	return len(m.versions[len(m.versions)-1].Rungs)
}

// Latest 返回最新版本，无任何版本时报 ErrNotReady。
func (m *Manifest) Latest() (Version, error) {
	if len(m.versions) == 0 {
		return Version{}, ErrNotReady
	}
	return copyVersion(m.versions[len(m.versions)-1]), nil
}

// Get 取历史版本，v 越界报 ErrVersionNotFound。
func (m *Manifest) Get(v int) (Version, error) {
	if v < 1 || v > len(m.versions) {
		return Version{}, ErrVersionNotFound
	}
	return copyVersion(m.versions[v-1]), nil
}

func copyVersion(v Version) Version {
	v.Rungs = append([]Entry(nil), v.Rungs...)
	return v
}
