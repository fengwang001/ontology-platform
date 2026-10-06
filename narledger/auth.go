package narledger

// grant 是一条左闭右开 [start, end) 的授权区间。
type grant struct {
	person string
	start  int64
	end    int64
}

func (g grant) validAt(t int64) bool { return g.start <= t && t < g.end }

// authBook 授权名单：支持登记、提前撤销、时刻有效性判定。
//
// 同一人允许登记多条区间（可交叠、可针对未来）。撤销在 at 处切断：
//   - 覆盖 at 的区间 end 收窄为 at（at 起立即失效）；
//   - start >= at 的未来区间整体删除；
//   - 早于 at 结束的历史区间原样保留。
type authBook struct {
	grants map[string][]grant
}

func newAuthBook() *authBook { return &authBook{} }

func (b *authBook) add(person string, start, end int64) {
	if b.grants == nil {
		b.grants = make(map[string][]grant)
	}
	b.grants[person] = append(b.grants[person], grant{person: person, start: start, end: end})
}

func (b *authBook) revoke(person string, at int64) *OpError {
	list, ok := b.grants[person]
	if !ok || len(list) == 0 {
		return opError("RevokeGrant", ErrNotFound, "人员 %s 无授权记录", person)
	}
	kept := list[:0]
	for _, g := range list {
		switch {
		case g.end <= at:
			kept = append(kept, g) // 历史区间
		case g.start >= at:
			// 未来区间删除
		default:
			g.end = at // 覆盖 at 的区间在 at 处闭合
			if g.start < g.end {
				kept = append(kept, g)
			}
		}
	}
	if len(kept) == 0 {
		delete(b.grants, person)
	} else {
		b.grants[person] = kept
	}
	return nil
}

func (b *authBook) validAt(person string, t int64) bool {
	for _, g := range b.grants[person] {
		if g.validAt(t) {
			return true
		}
	}
	return false
}
