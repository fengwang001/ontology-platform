// Package basket 定义 ETF 申赎清单（basket）的数据结构与参数校验。
package basket

import (
	"errors"
	"fmt"
	"sort"
)

// ErrParam 表示参数非法，可用 errors.Is 判定。
var ErrParam = errors.New("basket: invalid parameter")

// Flag 为现金替代标志。
type Flag byte

const (
	FlagN Flag = 'N' // 禁止现金替代
	FlagA Flag = 'A' // 允许现金替代
	FlagM Flag = 'M' // 必须现金替代
)

const (
	maxItems = 500
	maxQty   = 1_000_000
	maxPrem  = 5000
	maxFixed = 1_000_000_000_000
	maxE     = 1_000_000_000
	maxRmax  = 100
	maxQ     = 1_000_000
)

// Item 为清单项：sym 标的、qty 每申赎单位股数、flag 替代标志、
// prem 为 A 类溢价比例（基点）、fixed 为 M 类每单位固定替代金额。
type Item struct {
	Sym   string
	Qty   int64
	Flag  Flag
	Prem  int64
	Fixed int64
}

// Basket 为校验后的清单，Items 按 Sym 字节序排列。
type Basket struct {
	Items []Item
	E     int64 // 每单位现金差额，可为负
	Rmax  int64 // 现金替代比例上限（百分比）
	Q     int64 // 当日申购单位数上限
}

// New 校验参数并构造清单；任何非法参数返回包裹 ErrParam 的错误。
func New(items []Item, e, rmax, q int64) (*Basket, error) {
	if len(items) < 1 || len(items) > maxItems {
		return nil, fmt.Errorf("%w: items count %d", ErrParam, len(items))
	}
	if e < -maxE || e > maxE {
		return nil, fmt.Errorf("%w: E %d", ErrParam, e)
	}
	if rmax < 0 || rmax > maxRmax {
		return nil, fmt.Errorf("%w: Rmax %d", ErrParam, rmax)
	}
	if q < 1 || q > maxQ {
		return nil, fmt.Errorf("%w: Q %d", ErrParam, q)
	}
	seen := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it.Sym == "" {
			return nil, fmt.Errorf("%w: empty sym", ErrParam)
		}
		if _, dup := seen[it.Sym]; dup {
			return nil, fmt.Errorf("%w: duplicate sym %q", ErrParam, it.Sym)
		}
		seen[it.Sym] = struct{}{}
		if it.Qty < 1 || it.Qty > maxQty {
			return nil, fmt.Errorf("%w: qty %d for %q", ErrParam, it.Qty, it.Sym)
		}
		switch it.Flag {
		case FlagN:
		case FlagA:
			if it.Prem < 0 || it.Prem > maxPrem {
				return nil, fmt.Errorf("%w: prem %d for %q", ErrParam, it.Prem, it.Sym)
			}
		case FlagM:
			if it.Fixed < 1 || it.Fixed > maxFixed {
				return nil, fmt.Errorf("%w: fixed %d for %q", ErrParam, it.Fixed, it.Sym)
			}
		default:
			return nil, fmt.Errorf("%w: flag %q for %q", ErrParam, string(it.Flag), it.Sym)
		}
	}
	sorted := make([]Item, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sym < sorted[j].Sym })
	return &Basket{Items: sorted, E: e, Rmax: rmax, Q: q}, nil
}
