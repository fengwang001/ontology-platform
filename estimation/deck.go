package estimation

// Deck 为牌组：2 到 20 张互不相同、按声明次序严格递增的正整数
// 数值牌，外加两张固定特殊牌。特殊牌不属于 Deck.values，由
// Card.Special 识别。
type Deck struct {
	values []int       // 数值牌，严格递增
	pos    map[int]int // 数值 -> 在牌组中的位置（0 起）
}

// NewDeck 校验并构造牌组。
func NewDeck(values []int) (Deck, *Error) {
	d := Deck{}
	if len(values) < MinDeckSize || len(values) > MaxDeckSize {
		return d, newError(ErrInvalidParam, "牌组须包含 2 到 20 张数值牌")
	}
	d.values = make([]int, len(values))
	d.pos = make(map[int]int, len(values))
	for i, v := range values {
		if v <= 0 {
			return d, newError(ErrInvalidParam, "数值牌必须为正整数")
		}
		if i > 0 && v <= values[i-1] {
			return d, newError(ErrInvalidParam, "数值牌须互不相同且按声明次序严格递增")
		}
		d.values[i] = v
		d.pos[v] = i
	}
	return d, nil
}

// Contains 报告牌是否合法（数值牌在牌组内，或为两张特殊牌之一）。
func (d Deck) Contains(c Card) bool {
	if c.Special() {
		return true
	}
	_, ok := d.pos[int(c)]
	return ok
}

// Size 返回数值牌张数。
func (d Deck) Size() int { return len(d.values) }
