package coverage

import (
	"errors"
	"fmt"
)

// Verify 检查内部状态与规范视图的全部不变量，正常返回 nil。
func (c *Counter) Verify() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var errs []error
	if len(c.ends) != len(c.delta) {
		errs = append(errs, fmt.Errorf("ends/delta length mismatch: %d vs %d",
			len(c.ends), len(c.delta)))
	}

	// 端点严格升序、无重复，且每个端点都有非零增量。
	var run int64
	for i, p := range c.ends {
		if i > 0 && c.ends[i-1] >= p {
			errs = append(errs, fmt.Errorf("ends not strictly increasing at %d: %d !< %d",
				i, c.ends[i-1], p))
		}
		d, ok := c.delta[p]
		if !ok {
			errs = append(errs, fmt.Errorf("missing delta for endpoint %d", p))
		} else if d == 0 {
			errs = append(errs, fmt.Errorf("zero delta retained at %d", p))
		}
		run += d
		if run < 0 {
			errs = append(errs, fmt.Errorf("negative coverage %d at endpoint %d", run, p))
		}
		if i >= len(c.cum) || c.cum[i] != run {
			errs = append(errs, fmt.Errorf("prefix mismatch at endpoint %d", p))
		}
	}
	if len(c.ends) > 0 && run != 0 {
		errs = append(errs, fmt.Errorf("total delta not zero: %d", run))
	}

	// 用 refs 独立重算每个端点处的覆盖数，必须与前缀一致。
	for i, p := range c.ends {
		var want int64
		for k, n := range c.refs {
			if k.Lo <= p && p < k.Hi {
				want += n
			}
		}
		if i < len(c.cum) && c.cum[i] != want {
			errs = append(errs, fmt.Errorf(
				"coverage at %d is %d but refs imply %d", p, c.cum[i], want))
		}
	}

	if !c.dirty {
		var best int64
		for _, v := range c.cum {
			if v > best {
				best = v
			}
		}
		if best != c.max {
			errs = append(errs, fmt.Errorf("cached max %d != recomputed %d", c.max, best))
		}
	}

	if err := c.verifySegmentsLocked(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (c *Counter) verifySegmentsLocked() error {
	segs := c.SegmentsRlocked()
	for i, s := range segs {
		if s.Lo >= s.Hi {
			return fmt.Errorf("segment %d not a valid half-open range: [%d,%d)",
				i, s.Lo, s.Hi)
		}
		if s.Count <= 0 {
			return fmt.Errorf("zero/negative segment count at %d", s.Lo)
		}
		if i > 0 {
			prev := segs[i-1]
			if s.Lo < prev.Hi {
				return fmt.Errorf("segments overlap: [%d,%d) vs [%d,%d)",
					prev.Lo, prev.Hi, s.Lo, s.Hi)
			}
			if s.Lo == prev.Hi && s.Count == prev.Count {
				return fmt.Errorf("adjacent equal-count segments not merged at %d", s.Lo)
			}
		}
		// 段内覆盖数必须处处等于其声明值。
		idx := c.indexLocked(s.Lo)
		if idx < 0 || c.cum[idx] != s.Count {
			return fmt.Errorf("segment count mismatch at %d", s.Lo)
		}
	}
	return nil
}
