package migration

import (
	"context"
	"fmt"
	"sync"
)

// Version 是模式版本号，必须为正整数，从 1 开始。
type Version int

// Func 把数据从来源版本 from 迁移到 from+1。
// 入参 data 是存储内容的拷贝，迁移函数不得依赖跨调用共享的可变输入；
// 返回值是迁移后的新数据。返回错误时整个迁移中止且存储保持原样。
type Func func(ctx context.Context, from Version, data []byte) ([]byte, error)

// entry 是单个键的存储条目。
type entry struct {
	version Version
	data    []byte
}

// flight 表示同一键上正在进行（或刚完成）的一次迁移，用于合并并发读取。
type flight struct {
	done chan struct{}
	res  []byte
	err  error
	// target 是发起本次迁移时的当前版本，写回成功即表明这些步骤已完成。
	target Version
}

// Store 是键控状态的惰性模式迁移存储。零值不可用，必须用 NewStore 创建。
// Store 的所有方法都可以被多个执行体并发调用。
type Store struct {
	mu      sync.RWMutex
	current Version
	data    map[string]entry
	funcs   map[Version]Func
	flights map[string]*flight
	gates   map[string]*sync.Mutex
	log     Logger
}

// Option 配置新建的 Store。
type Option func(*Store)

// WithLogger 设置步骤日志记录器；默认使用标准库 log.Printf。传 nil 被忽略。
func WithLogger(l Logger) Option {
	return func(s *Store) {
		if l != nil {
			s.log = l
		}
	}
}

// NewStore 创建存储，初始当前版本为 initial（必须为正）。
func NewStore(initial Version, opts ...Option) (*Store, error) {
	if initial < 1 {
		return nil, fmt.Errorf("%w: initial version %d", ErrInvalidVersion, initial)
	}
	s := &Store{
		current: initial,
		data:    make(map[string]entry),
		funcs:   make(map[Version]Func),
		flights: make(map[string]*flight),
		gates:   make(map[string]*sync.Mutex),
		log:     defaultLogger{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// CurrentVersion 返回当前模式版本。
func (s *Store) CurrentVersion() Version {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Register 登记一条来源版本为 from 的迁移函数（from -> from+1）。
// 同一来源版本只能登记一次，函数不得为 nil。登记失败不改变任何已有登记。
func (s *Store) Register(from Version, fn Func) error {
	if from < 1 {
		return fmt.Errorf("%w: source version %d", ErrInvalidVersion, from)
	}
	if fn == nil {
		return fmt.Errorf("%w: migration function for v%d is nil", ErrInvalidArgument, from)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.funcs[from]; exists {
		return fmt.Errorf("%w: source version %d", ErrMigrationExists, from)
	}
	s.funcs[from] = fn
	s.log.Printf("migration: register step v%d->v%d: decision=accepted", from, from+1)
	return nil
}

// Upgrade 把当前模式版本升级到 next，不触碰任何键。
// next 必须为正且严格大于当前版本。
func (s *Store) Upgrade(next Version) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if next < 1 {
		return fmt.Errorf("%w: target version %d", ErrInvalidVersion, next)
	}
	if next <= s.current {
		return fmt.Errorf("%w: target %d is not greater than current %d", ErrInvalidVersion, next, s.current)
	}
	prev := s.current
	s.current = next
	s.log.Printf("migration: upgrade current v%d->v%d: keys_touched=0 decision=accepted", prev, next)
	return nil
}

// Write 以当前版本存储 data 的拷贝。若该键上有迁移正在进行，则等待其结束，
// 以免普通写并发于迁移写回。
func (s *Store) Write(ctx context.Context, key string, data []byte) error {
	if key == "" {
		return ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 若该键上有迁移在进行，先等待其结束；不能持门控等待，因为迁移的
	// 写回需要门控。等待结束后取门控并复查，处理“刚升级又触发新迁移”的窗口。
	for {
		gate := s.gateFor(key)
		gate.Lock()
		s.mu.RLock()
		f := s.flights[key]
		s.mu.RUnlock()
		if f == nil {
			defer gate.Unlock()
			break
		}
		gate.Unlock()
		s.log.Printf("migration: write key=%q: decision=wait_inflight", key)
		select {
		case <-f.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	s.mu.Lock()
	version := s.current
	s.data[key] = entry{version: version, data: cloneBytes(data)}
	s.mu.Unlock()
	s.log.Printf("migration: write key=%q version=v%d bytes=%d: decision=stored", key, version, len(data))
	return nil
}

// Read 读取键：
//   - 存储版本等于当前版本：直接返回数据拷贝；
//   - 存储版本落后：先检查整条迁移链，链不完整则不调用任何函数并返回
//     ErrMissingMigration；链完整则从存储版本之后的步骤开始依次调用，
//     任一步失败则存储保持原样，全部成功则原子写回并返回结果；
//   - 同一键的并发读取共用同一次迁移执行，结果逐字节一致。
func (s *Store) Read(ctx context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, ErrInvalidKey
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for {
		s.mu.RLock()
		e, ok := s.data[key]
		if !ok {
			s.mu.RUnlock()
			s.log.Printf("migration: read key=%q: decision=reject reason=key_not_found", key)
			return nil, fmt.Errorf("%w: %q", ErrKeyNotFound, key)
		}
		cur := s.current
		if e.version == cur {
			s.mu.RUnlock()
			s.log.Printf("migration: read key=%q stored=v%d current=v%d: decision=fast_path", key, e.version, cur)
			return cloneBytes(e.data), nil
		}
		if e.version > cur {
			s.mu.RUnlock()
			return nil, fmt.Errorf("%w: stored v%d is ahead of current v%d for %q", ErrInvalidVersion, e.version, cur, key)
		}

		// 已有同键迁移在进行：加入等待，共用其结果。
		if f := s.flights[key]; f != nil {
			s.mu.RUnlock()
			s.log.Printf("migration: read key=%q stored=v%d current=v%d: decision=join_inflight", key, e.version, cur)
			select {
			case <-f.done:
				if f.err != nil {
					return nil, f.err
				}
				return cloneBytes(f.res), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		s.mu.RUnlock()

		// 没有进行中的迁移：尝试成为执行者。门控串行化同键的迁移与普通写。
		gate := s.gateFor(key)
		gate.Lock()
		s.mu.Lock()

		// 持门控与数据锁后重新观察状态（期间版本可能再次升级）。
		e2, ok := s.data[key]
		if !ok {
			s.mu.Unlock()
			gate.Unlock()
			continue // 回到循环顶部，规范地返回 ErrKeyNotFound
		}
		cur2 := s.current
		if e2.version == cur2 {
			s.mu.Unlock()
			gate.Unlock()
			s.log.Printf("migration: read key=%q stored=v%d current=v%d: decision=fast_path", key, e2.version, cur2)
			return cloneBytes(e2.data), nil
		}
		if e2.version > cur2 {
			s.mu.Unlock()
			gate.Unlock()
			return nil, fmt.Errorf("%w: stored v%d is ahead of current v%d for %q", ErrInvalidVersion, e2.version, cur2, key)
		}
		missing := Version(0)
		for v := e2.version; v < cur2; v++ {
			if _, ok := s.funcs[v]; !ok {
				missing = v
				break
			}
		}
		if missing != 0 {
			s.mu.Unlock()
			gate.Unlock()
			s.log.Printf("migration: read key=%q stored=v%d current=v%d: decision=reject reason=missing_step v%d->v%d calls=0",
				key, e2.version, cur2, missing, missing+1)
			return nil, fmt.Errorf("%w: no function registered for source version %d (chain v%d->v%d for %q)",
				ErrMissingMigration, missing, e2.version, cur2, key)
		}
		if f := s.flights[key]; f != nil {
			// 理论上持门控时不会出现；防御性地转为加入等待。
			s.mu.Unlock()
			gate.Unlock()
			s.log.Printf("migration: read key=%q: decision=join_inflight_after_gate", key)
			select {
			case <-f.done:
				if f.err != nil {
					return nil, f.err
				}
				return cloneBytes(f.res), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// 成为本次迁移的执行者：快照数据与步骤函数，只迁移存储版本之后的步骤。
		start := e2.version
		snapshot := cloneBytes(e2.data)
		steps := make([]Func, 0, cur2-start)
		for v := start; v < cur2; v++ {
			steps = append(steps, s.funcs[v])
		}
		f := &flight{done: make(chan struct{}), target: cur}
		s.flights[key] = f
		s.mu.Unlock()

		s.log.Printf("migration: read key=%q stored=v%d current=v%d: decision=run_chain steps=%d", key, start, cur2, len(steps))
		// 同步执行迁移链；调用方持有门控，写回天然与普通写互斥。
		res, merr := s.executeMigration(ctx, key, start, snapshot, steps, f)
		gate.Unlock()
		if merr != nil {
			return nil, merr
		}
		return cloneBytes(res), nil
	}
}

// executeMigration 依次执行迁移步骤；全部成功后原子写回，任一步失败则存储保持原样。
// 调用方必须持有该键的门控锁。flight 在返回前被关闭并从表中移除。
func (s *Store) executeMigration(ctx context.Context, key string, start Version, data []byte, steps []Func, f *flight) (result []byte, merr error) {
	result = data
	defer func() {
		if r := recover(); r != nil {
			merr = fmt.Errorf("%w: step panicked: %v", ErrMigrationFailed, r)
			f.err = merr
			s.log.Printf("migration: step key=%q: decision=panic value=%v", key, r)
		}
		s.mu.Lock()
		if merr == nil {
			f.res = cloneBytes(result)
		}
		delete(s.flights, key)
		s.mu.Unlock()
		close(f.done)
	}()

	v := start
	for _, fn := range steps {
		if err := ctx.Err(); err != nil {
			merr = err
			f.err = err
			s.log.Printf("migration: step key=%q v%d->v%d: decision=abort reason=context error=%v", key, v, v+1, err)
			return nil, err
		}
		s.log.Printf("migration: step key=%q v%d->v%d: input_bytes=%d", key, v, v+1, len(result))
		out, err := fn(ctx, v, cloneBytes(result))
		if err != nil {
			merr = fmt.Errorf("%w: step v%d->v%d: %w", ErrMigrationFailed, v, v+1, err)
			f.err = merr
			s.log.Printf("migration: step key=%q v%d->v%d: return_error=%v: decision=keep_storage_unchanged",
				key, v, v+1, err)
			return nil, merr
		}
		result = out
		s.log.Printf("migration: step key=%q v%d->v%d: return_bytes=%d: decision=ok", key, v, v+1, len(out))
		v++
	}

	// 原子写回：调用方持门控（没有普通写并发），持数据锁以一次 map 赋值同时替换版本与数据。
	s.mu.Lock()
	if e, ok := s.data[key]; ok && e.version >= f.target {
		// 键已被更新版本的写入/迁移覆盖，本次结果作废，不回退覆盖者。
		s.mu.Unlock()
		s.log.Printf("migration: writeback key=%q: decision=skip reason=stored_v%d_ahead_of_target_v%d", key, e.version, f.target)
		return result, nil
	}
	writtenVersion := s.current
	s.data[key] = entry{version: writtenVersion, data: cloneBytes(result)}
	s.mu.Unlock()

	s.log.Printf("migration: writeback key=%q stored=v%d->v%d bytes=%d: decision=committed", key, start, writtenVersion, len(result))
	return result, nil
}

// StoredVersion 返回键的存储版本，但不触发迁移。
func (s *Store) StoredVersion(key string) (Version, error) {
	if key == "" {
		return 0, ErrInvalidKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.data[key]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrKeyNotFound, key)
	}
	return e.version, nil
}

// Exists 报告键是否存在，但不触发迁移。
func (s *Store) Exists(key string) (bool, error) {
	if key == "" {
		return false, ErrInvalidKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok, nil
}

// SelfCheck 检查从当前数据中最低存储版本到当前版本所需的整条迁移链是否完整。
// 空存储视为完整（当前版本到当前版本之间没有步骤）。
func (s *Store) SelfCheck(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	min := s.current
	for _, e := range s.data {
		if e.version < min {
			min = e.version
		}
	}
	for v := min; v < s.current; v++ {
		if _, ok := s.funcs[v]; !ok {
			s.log.Printf("migration: self-check current=v%d: decision=reject reason=missing_step v%d->v%d", s.current, v, v+1)
			return fmt.Errorf("%w: no function registered for source version %d", ErrMissingMigration, v)
		}
	}
	s.log.Printf("migration: self-check current=v%d lowest_stored=v%d: decision=chain_complete", s.current, min)
	return nil
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp
}

// gateFor 返回某键专用的门控锁，串行化该键的写与迁移写回。
func (s *Store) gateFor(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.gates[key]
	if !ok {
		g = &sync.Mutex{}
		s.gates[key] = g
	}
	return g
}
