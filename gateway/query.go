package gateway

import (
	"ontology/record"
)

func replayOutcome(rec *record.Record) Outcome {
	return Outcome{
		Result:   rec.Result(),
		Err:      rec.Err(),
		Replayed: true,
		State:    rec.State(),
	}
}

func conflictOutcome(key string, rec *record.Record) Outcome {
	return Outcome{
		Err:   &ConflictError{Key: key},
		State: rec.State(),
	}
}

// Lookup 查询某键当前状态、剩余存活时长与是否已有成功结果。
// 不存在或已过期的键返回零值 Info（Known=false），绝不返回残留状态。
func (g *Gateway) Lookup(key string) Info {
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.store[key]
	if !ok || rec.ExpiredAt(g.now()) {
		return Info{}
	}
	return Info{
		Known:     true,
		State:     rec.State(),
		Remaining: rec.TTL(g.now()),
		HasResult: rec.State() == record.Succeeded,
	}
}

// Delete 主动清理一个键；主要供需要即时回收的调用方使用。
func (g *Gateway) Delete(key string) {
	g.mu.Lock()
	delete(g.store, key)
	g.mu.Unlock()
}
