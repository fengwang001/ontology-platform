package lessor

import (
	"fmt"
	"sort"
)

// Grant 授予租约：仅主。g = max(ttl, MinTTL)，x = now+g。返回有效 TTL g。
func (l *Lessor) Grant(id, ttl, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkID(id); err != nil {
		return 0, err
	}
	if ttl < 1 || ttl > l.cfg.MaxTTL {
		return 0, fmt.Errorf("%w: ttl %d out of [1, MaxTTL=%d]", ErrInvalidParam, ttl, l.cfg.MaxTTL)
	}
	if err := l.checkTime(now); err != nil {
		return 0, err
	}
	if err := l.checkPrimary(); err != nil {
		return 0, err
	}
	if _, ok := l.leases[id]; ok {
		return 0, fmt.Errorf("%w: id %d", ErrLeaseExists, id)
	}
	g := ttl
	if g < l.cfg.MinTTL {
		g = l.cfg.MinTTL
	}
	le := &Lease{id: id, g: g, x: now + g, keys: make(map[string]struct{})}
	l.leases[id] = le
	l.h.push(le)
	l.T = now
	return g, nil
}

// Renew 续约：仅主。x <= now 时报已过期（租约仍留在积压中）；
// 成功则 x = now+g、sv 清零，返回 g。
func (l *Lessor) Renew(id, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkID(id); err != nil {
		return 0, err
	}
	if err := l.checkTime(now); err != nil {
		return 0, err
	}
	if err := l.checkPrimary(); err != nil {
		return 0, err
	}
	le, ok := l.leases[id]
	if !ok {
		return 0, fmt.Errorf("%w: id %d", ErrLeaseNotFound, id)
	}
	if le.x <= now {
		return 0, fmt.Errorf("%w: id %d x=%d now=%d", ErrExpired, id, le.x, now)
	}
	le.x = now + le.g
	le.sv = 0
	l.h.fix(le)
	l.T = now
	return le.g, nil
}

// Attach 挂靠键：仅主。key 已在该租约上则无操作成功；租约已满报 ErrFull
// （先判，不摘除）；否则把 key 从原租约摘除（若有）再挂到 id。
func (l *Lessor) Attach(key string, id, now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidParam)
	}
	if err := l.checkID(id); err != nil {
		return err
	}
	if err := l.checkTime(now); err != nil {
		return err
	}
	if err := l.checkPrimary(); err != nil {
		return err
	}
	le, ok := l.leases[id]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrLeaseNotFound, id)
	}
	if le.x <= now {
		return fmt.Errorf("%w: id %d x=%d now=%d", ErrExpired, id, le.x, now)
	}
	if _, ok := le.keys[key]; ok {
		l.T = now
		return nil // 已在该租约上，无操作成功
	}
	if int64(len(le.keys)) >= l.cfg.Kmax {
		return fmt.Errorf("%w: id %d has %d keys", ErrFull, id, len(le.keys))
	}
	if old, ok := l.byKey[key]; ok {
		delete(l.leases[old].keys, key)
	}
	le.keys[key] = struct{}{}
	l.byKey[key] = id
	l.T = now
	return nil
}

// Tick 撤销：仅主。取全部 x <= now 的租约，按 (x, id) 升序撤销前 R 个，
// 其余留作积压。返回 (id, 键列表) 序列，键升序。
func (l *Lessor) Tick(now int64) ([]Revoked, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return nil, err
	}
	if err := l.checkPrimary(); err != nil {
		return nil, err
	}
	l.tickInspected = 0
	var out []Revoked
	for int64(len(out)) < l.cfg.R && l.h.Len() > 0 {
		top := l.h.peek()
		l.tickInspected++
		if top.x > now {
			break
		}
		l.h.pop()
		out = append(out, l.dropLease(top))
	}
	l.T = now
	return out, nil
}

// Revoke 撤销指定租约：仅主。租约存在即撤销，无视到期，不计入 R。
// 返回被摘下的键（升序）。
func (l *Lessor) Revoke(id, now int64) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkID(id); err != nil {
		return nil, err
	}
	if err := l.checkTime(now); err != nil {
		return nil, err
	}
	if err := l.checkPrimary(); err != nil {
		return nil, err
	}
	le, ok := l.leases[id]
	if !ok {
		return nil, fmt.Errorf("%w: id %d", ErrLeaseNotFound, id)
	}
	l.h.remove(le)
	rev := l.dropLease(le)
	l.T = now
	return rev.Keys, nil
}

// Checkpoint 检查点：仅主。对 x > now 的租约令 sv = x-now，其余不变。
func (l *Lessor) Checkpoint(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if err := l.checkPrimary(); err != nil {
		return err
	}
	for _, le := range l.leases {
		if le.x > now {
			le.sv = le.x - now
		}
	}
	l.T = now
	return nil
}

// Demote 主变从，不改任何租约。
func (l *Lessor) Demote(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if !l.primary {
		return fmt.Errorf("%w: already follower", ErrRole)
	}
	l.primary = false
	l.T = now
	return nil
}

// Promote 从变主：对每个租约（含积压中的）令
// x = now + E + (sv > 0 时取 sv，否则取 g)。不清 sv。
func (l *Lessor) Promote(now int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkTime(now); err != nil {
		return err
	}
	if l.primary {
		return fmt.Errorf("%w: already primary", ErrRole)
	}
	l.primary = true
	for _, le := range l.leases {
		base := le.g
		if le.sv > 0 {
			base = le.sv
		}
		le.x = now + l.cfg.E + base
	}
	l.h.rebuild()
	l.T = now
	return nil
}

// TTL 只读查询（now 须合法但不改 T）：
// 主返回 max(x-now, 0)，从返回 sv > 0 时的 sv 否则 g。
func (l *Lessor) TTL(id, now int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkID(id); err != nil {
		return 0, err
	}
	if err := l.checkTime(now); err != nil {
		return 0, err
	}
	le, ok := l.leases[id]
	if !ok {
		return 0, fmt.Errorf("%w: id %d", ErrLeaseNotFound, id)
	}
	if l.primary {
		if d := le.x - now; d > 0 {
			return d, nil
		}
		return 0, nil
	}
	if le.sv > 0 {
		return le.sv, nil
	}
	return le.g, nil
}

// dropLease 删除租约并摘下其全部键，返回撤销结果（键升序）。
// 调用方负责从堆中移除。
func (l *Lessor) dropLease(le *Lease) Revoked {
	keys := make([]string, 0, len(le.keys))
	for k := range le.keys {
		keys = append(keys, k)
		delete(l.byKey, k)
	}
	sort.Strings(keys)
	delete(l.leases, le.id)
	return Revoked{ID: le.id, Keys: keys}
}
