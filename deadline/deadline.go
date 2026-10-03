package deadline

import "errors"

var ErrConfig = errors.New("deadline: invalid config")

type Kind int

const (
	SC Kind = iota
	S2C
	HB
	S2S
	Wake
)

func (k Kind) String() string {
	switch k {
	case SC:
		return "sc"
	case S2C:
		return "s2c"
	case HB:
		return "hb"
	case S2S:
		return "s2s"
	default:
		return "wake"
	}
}

type Config struct {
	S2S int64
	S2C int64
	HB  int64
	SC  int64
}

// NewConfig 校验四类时限：0 表示不设，否则 ∈[1,1e9]；s2c 与 sc 至少一个非零。
func NewConfig(s2s, s2c, hb, sc int64) (Config, error) {
	for _, v := range []int64{s2s, s2c, hb, sc} {
		if v < 0 || v > 1_000_000_000 {
			return Config{}, ErrConfig
		}
	}
	if s2c == 0 && sc == 0 {
		return Config{}, ErrConfig
	}
	return Config{S2S: s2s, S2C: s2c, HB: hb, SC: sc}, nil
}

// At 表示一个到期项。
type At struct {
	Time int64
	Kind Kind
}

func with(cfg Config, t int64, k Kind, out []At) []At {
	if t > 0 {
		return append(out, At{Time: t, Kind: k})
	}
	return out
}

// ScheduledItems 推导排队态到期项：s2s=g+s2s，sc=t0+sc。
func ScheduledItems(cfg Config, t0, g int64) []At {
	var out []At
	if cfg.S2S > 0 {
		out = append(out, At{Time: g + cfg.S2S, Kind: S2S})
	}
	return with(cfg, t0+cfg.SC, SC, out)
}

// RunningItems 推导执行态到期项：s2c=r+s2c、hb=h+hb、sc=t0+sc。
func RunningItems(cfg Config, t0, r, h int64) []At {
	var out []At
	if cfg.S2C > 0 {
		out = append(out, At{Time: r + cfg.S2C, Kind: S2C})
	}
	if cfg.HB > 0 {
		out = append(out, At{Time: h + cfg.HB, Kind: HB})
	}
	return with(cfg, t0+cfg.SC, SC, out)
}

// WaitingItems 推导退避态到期项：sc 与内部 wake=g'（退避结束、重新排队）。
// 同刻时 SC 优先于 Wake（见 Heap）。
func WaitingItems(cfg Config, t0, wake int64) []At {
	out := with(cfg, t0+cfg.SC, SC, nil)
	return append(out, At{Time: wake, Kind: Wake})
}
