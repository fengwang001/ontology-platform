// Package ladder 推导单个点播转码作业的码率阶梯。
package ladder

import "errors"

// ErrInvalidTemplate 模板档数、字段取值或单调性非法。
var ErrInvalidTemplate = errors.New("ladder: invalid template")

// ErrSourceInsufficient 第一步（按源高度截断）丢失了 required 档。
var ErrSourceInsufficient = errors.New("ladder: source insufficient for required rung")

// Rung 是阶梯中的一档（码率为按源码率钳制后的值）。
type Rung struct {
	Name     string
	Height   int
	Bitrate  int
	Required bool
}

// Template 是转码模板，构造后视为只读。
type Template struct {
	Rungs []Rung
}

// NewTemplate 校验模板并返回模板。
func NewTemplate(rungs []Rung) (*Template, error) {
	if len(rungs) < 1 || len(rungs) > 16 {
		return nil, ErrInvalidTemplate
	}
	for i := range rungs {
		r := rungs[i]
		if r.Name == "" || r.Height < 1 || r.Height > 4320 ||
			r.Bitrate < 1 || r.Bitrate > 1e8 {
			return nil, ErrInvalidTemplate
		}
		if i > 0 && (r.Height <= rungs[i-1].Height || r.Bitrate <= rungs[i-1].Bitrate) {
			return nil, ErrInvalidTemplate
		}
	}
	hasRequired := false
	for _, r := range rungs {
		if r.Required {
			hasRequired = true
			break
		}
	}
	if !hasRequired {
		return nil, ErrInvalidTemplate
	}
	cp := make([]Rung, len(rungs))
	copy(cp, rungs)
	return &Template{Rungs: cp}, nil
}

// Derive 按“截高 → 钳码率 → 保严格递增”三步推导作业阶梯。
func (t *Template) Derive(srcHeight, srcBitrate int) ([]Rung, error) {
	if srcHeight < 1 || srcHeight > 4320 || srcBitrate < 1 || srcBitrate > 1e8 {
		return nil, ErrInvalidTemplate
	}
	// 第一步：丢弃高度大于源高度的档（恰等保留）；丢到 required 即源不足。
	cut := make([]Rung, 0, len(t.Rungs))
	for _, r := range t.Rungs {
		if r.Height <= srcHeight {
			cut = append(cut, r)
		} else if r.Required {
			return nil, ErrSourceInsufficient
		}
	}
	// 第二步：码率钳制到源码率。
	for i := range cut {
		if cut[i].Bitrate > srcBitrate {
			cut[i].Bitrate = srcBitrate
		}
	}
	// 第三步：自低到高，码率不严格大于上一个被保留档则丢弃（最低档总保留）。
	out := make([]Rung, 0, len(cut))
	for _, r := range cut {
		if len(out) == 0 || r.Bitrate > out[len(out)-1].Bitrate {
			out = append(out, r)
		}
	}
	return out, nil
}
