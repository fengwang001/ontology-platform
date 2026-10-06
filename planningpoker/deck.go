package planningpoker

import "strconv"

// Card 表示一张可投的牌。
//
// 数值牌为 Config.Cards 中声明的牌（Special == false）；
// 另固定附带两张特殊牌：不确定与需要休息，特殊牌计入“已投”，
// 但不参与揭示后的统计。
type Card struct {
	Value   int
	Special bool
	Name    string
}

// Deck 为会话使用的牌组：按声明次序严格递增的数值牌 + 两张特殊牌。
type Deck struct {
	numeric []Card
	special []Card
	byKey   map[cardKey]int // key -> numeric 下标（特殊牌为 -1）
}

type cardKey struct {
	value   int
	special bool
}

// 两张固定特殊牌。其 Value 仅用于身份区分，不参与统计。
var (
	CardUncertain = Card{Value: -1, Special: true, Name: "uncertain"}
	CardBreak     = Card{Value: -2, Special: true, Name: "break"}
)

// specialCards 顺序固定，保证重放分布完全一致。
var specialCards = []Card{CardUncertain, CardBreak}

func newDeck(numericValues []int) (*Deck, error) {
	if len(numericValues) < 2 || len(numericValues) > 20 {
		return nil, ErrInvalidArgument
	}
	d := &Deck{
		numeric: make([]Card, len(numericValues)),
		special: append([]Card(nil), specialCards...),
		byKey:   make(map[cardKey]int, len(numericValues)+len(specialCards)),
	}
	prev := 0
	for i, v := range numericValues {
		if v <= 0 || v <= prev {
			return nil, ErrInvalidArgument
		}
		prev = v
		d.numeric[i] = Card{Value: v, Special: false, Name: strconv.Itoa(v)}
		d.byKey[cardKey{value: v, special: false}] = i
	}
	for _, c := range specialCards {
		// 特殊牌只用 Special 标志区分（Value 仅为展示用的哨兵），
		// 避免负数与数值牌的正 Value 在同一 map 中发生键碰撞。
		d.byKey[cardKey{special: true, value: specialSlot(c)}] = -1
	}
	return d, nil
}

// indexOf 返回牌在牌组中的位置：数值牌返回其声明下标（从 0 起），
// 特殊牌返回 -1；不在牌组中返回 -2。
func (d *Deck) indexOf(c Card) int {
	var key cardKey
	if c.Special {
		key = cardKey{special: true, value: specialSlot(c)}
	} else {
		key = cardKey{special: false, value: c.Value}
	}
	idx, ok := d.byKey[key]
	if !ok {
		return -2
	}
	return idx
}

// specialSlot 以固定顺序区分两张特殊牌：uncertain=0, break=1。
func specialSlot(c Card) int {
	for i, sc := range specialCards {
		if c == sc {
			return i
		}
	}
	return -1
}

func (d *Deck) numericCount() int { return len(d.numeric) }

// allCards 按“数值牌升序 + 两张固定特殊牌”的固定顺序返回全部牌，
// 供揭示分布以确定性顺序输出。
func (d *Deck) allCards() []Card {
	out := make([]Card, 0, len(d.numeric)+len(d.special))
	out = append(out, d.numeric...)
	out = append(out, d.special...)
	return out
}
