package ledger

// authRecord 记录某人员的授权窗口 [from, until)，左闭右开。
// 提前撤销通过把 until 钳到撤销时刻实现：撤销时刻起即失效，
// 已完成的领用与销毁不受影响。
type authRecord struct {
	from  int64
	until int64
}

func (r authRecord) validAt(t int64) bool {
	return r.from <= t && t < r.until
}

// authRegistry 是授权名单，按人员记录生效与失效时刻。
type authRegistry struct {
	records map[string]authRecord
}

func newAuthRegistry() *authRegistry {
	return &authRegistry{records: make(map[string]authRecord)}
}

func (a *authRegistry) grant(person string, from, until int64) {
	a.records[person] = authRecord{from: from, until: until}
}

func (a *authRegistry) revoke(person string, now int64) {
	r := a.records[person]
	if now < r.until {
		r.until = now
		a.records[person] = r
	}
}

func (a *authRegistry) has(person string) bool {
	_, ok := a.records[person]
	return ok
}

func (a *authRegistry) validAt(person string, t int64) bool {
	r, ok := a.records[person]
	return ok && r.validAt(t)
}
