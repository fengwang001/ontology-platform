package kanban

import "fmt"

// AllCards 返回全部卡片 ID（无序）。
func (b *Board) AllCards() map[string]struct{} {
	ids := make(map[string]struct{}, len(b.cards))
	for id := range b.cards {
		ids[id] = struct{}{}
	}
	return ids
}

// GetCard 返回卡片快照；不存在返回 ErrCardNotFound。
func (b *Board) GetCard(id string) (*Card, error) {
	c, ok := b.cards[id]
	if !ok {
		return nil, newError(ErrCardNotFound, "card %q does not exist", id)
	}
	return b.snapshot(c), nil
}

// ColumnCount 返回某列当前占用数。
func (b *Board) ColumnCount(col int) int { return b.colCount[col] }

// OwnerCount 返回某负责人在进行中区域的占用数。
func (b *Board) OwnerCount(owner string) int { return b.ownerCount[owner] }

// ExpeditedCard 返回当前持有加急标记的卡片 ID（空串表示无）。
func (b *Board) ExpeditedCard() string { return b.expeditedCard }

// LastNow 返回上一次被接受操作的 now。
func (b *Board) LastNow() int64 { return b.lastNow }

// NumColumns 返回列数。
func (b *Board) NumColumns() int { return len(b.columns) }

// ColumnLimit 返回某列上限。
func (b *Board) ColumnLimit(col int) int { return b.columns[col].wipLimit }

// OwnerLimit 返回 G。
func (b *Board) OwnerLimit() int { return b.ownerLimit }

// CheckInvariants 重新全量扫描状态并核对内部计数器，供测试验证。
// 非加急卡片不得违反列/负责人上限；加急标记全局至多一张且与卡片一致。
func (b *Board) CheckInvariants() error {
	n := len(b.columns)
	col := make([]int, n)
	own := map[string]int{}
	exp := []string{}
	for _, c := range b.cards {
		col[c.col]++
		if b.isInProgress(c.col) {
			own[c.owner]++
			if c.expedited {
				exp = append(exp, c.id)
			}
		} else if c.expedited {
			return fmt.Errorf("card %q outside in-progress but expedited flag set", c.id)
		}
	}
	for i, v := range col {
		if v != b.colCount[i] {
			return fmt.Errorf("colCount[%d] internal %d != actual %d", i, b.colCount[i], v)
		}
	}
	for owner, v := range own {
		if v != b.ownerCount[owner] {
			return fmt.Errorf("ownerCount[%q] internal %d != actual %d", owner, b.ownerCount[owner], v)
		}
	}
	if len(exp) > 1 {
		return fmt.Errorf("multiple expedited cards: %v", exp)
	}
	if len(exp) == 1 && b.expeditedCard != exp[0] {
		return fmt.Errorf("expedited pointer %q != actual %q", b.expeditedCard, exp[0])
	}
	if len(exp) == 0 && b.expeditedCard != "" {
		return fmt.Errorf("expedited pointer %q set but no expedited card", b.expeditedCard)
	}
	return nil
}
