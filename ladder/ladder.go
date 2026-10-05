// Package ladder 校验转码模板并按源参数推导作业的码率阶梯。
package ladder

import "errors"

var (
	// ErrInvalidTemplate 模板参数非法（档数、高度、码率、递增性、required、R、C 等）。
	ErrInvalidTemplate = errors.New("ladder: 模板参数非法")
	// ErrSourceInsufficient 源不足：required 档在第一步（按源高裁剪）被丢弃。
	ErrSourceInsufficient = errors.New("ladder: 源不足")
)

const (
	MaxRungs   = 16
	MaxHeight  = 4320
	MaxBitrate = 100_000_000
	MaxRetry   = 5
	MaxConc    = 16
)

// Rung 模板或阶梯中的一档。阶梯中的 Bitrate 为按源码率钳制后的值。
type Rung struct {
	Name     string
	Height   int
	Bitrate  int
	Required bool
}

// Template 校验通过的转码模板。
type Template struct {
	rungs []Rung
	retry int
	conc  int
}

// NewTemplate 构造模板：1..16 档，name 非空且唯一，height 1..4320、
// bitrate 1..1e8 且二者均严格递增，至少一个 required，R 0..5，C 1..16。
func NewTemplate(rungs []Rung, retryLimit, concurrency int) (Template, error) {
	if len(rungs) < 1 || len(rungs) > MaxRungs {
		return Template{}, ErrInvalidTemplate
	}
	if retryLimit < 0 || retryLimit > MaxRetry || concurrency < 1 || concurrency > MaxConc {
		return Template{}, ErrInvalidTemplate
	}
	seen := make(map[string]bool, len(rungs))
	hasRequired := false
	for i, r := range rungs {
		if r.Name == "" || seen[r.Name] {
			return Template{}, ErrInvalidTemplate
		}
		seen[r.Name] = true
		if r.Height < 1 || r.Height > MaxHeight || r.Bitrate < 1 || r.Bitrate > MaxBitrate {
			return Template{}, ErrInvalidTemplate
		}
		if i > 0 && (r.Height <= rungs[i-1].Height || r.Bitrate <= rungs[i-1].Bitrate) {
			return Template{}, ErrInvalidTemplate
		}
		hasRequired = hasRequired || r.Required
	}
	if !hasRequired {
		return Template{}, ErrInvalidTemplate
	}
	return Template{rungs: append([]Rung(nil), rungs...), retry: retryLimit, conc: concurrency}, nil
}

// Rungs 返回模板档位的副本。
func (t Template) Rungs() []Rung { return append([]Rung(nil), t.rungs...) }

// RetryLimit 重试上限 R。
func (t Template) RetryLimit() int { return t.retry }

// Concurrency 单作业并行上限 C。
func (t Template) Concurrency() int { return t.conc }

// Ladder 一次 Submit 推导出的作业阶梯，档位按高度升序。
type Ladder struct {
	rungs []Rung
}

// Rungs 返回阶梯档位的副本（Bitrate 为钳制后的值）。
func (l Ladder) Rungs() []Rung { return append([]Rung(nil), l.rungs...) }

// Derive 三步推导：1) 丢弃 height > srcHeight 的档，required 被丢则报源不足；
// 2) 码率钳制为 min(bitrate, srcBitrate)；3) 自低到高丢弃码率不严格大于
// 上一被保留档的档（最低档总保留，此步被丢的 required 档不算错误）。
func (t Template) Derive(srcHeight, srcBitrate int) (Ladder, error) {
	kept := make([]Rung, 0, len(t.rungs))
	for _, r := range t.rungs {
		if r.Height > srcHeight {
			if r.Required {
				return Ladder{}, ErrSourceInsufficient
			}
			continue
		}
		if r.Bitrate > srcBitrate {
			r.Bitrate = srcBitrate
		}
		kept = append(kept, r)
	}
	out := kept[:0]
	for i, r := range kept {
		if i > 0 && r.Bitrate <= out[len(out)-1].Bitrate {
			continue
		}
		out = append(out, r)
	}
	return Ladder{rungs: append([]Rung(nil), out...)}, nil
}
