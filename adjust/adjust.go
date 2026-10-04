package adjust

import "sync"

// DB 保存库位账面数、单价与累计净移动量。
type DB struct {
	tabs int64
	tpct int64
	lim  int64
	mu   sync.Mutex
	locs map[string]*loc
}

type loc struct {
	book  int64
	price int64
	mv    int64
}

func New(tabs, tpct, lim int64) *DB {
	return &DB{tabs: tabs, tpct: tpct, lim: lim, locs: make(map[string]*loc)}
}

const maxBook = int64(1_000_000_000)

func (db *DB) AddLoc(id []byte, book, price int64) error {
	if !validID(id) || book < 0 || book > maxBook || price < 0 || price > 1_000_000 {
		return ErrInvalid
	}
	key := string(id)
	db.mu.Lock()
	defer db.mu.Unlock()
	if _, ok := db.locs[key]; ok {
		return ErrConflict
	}
	db.locs[key] = &loc{book: book, price: price}
	return nil
}

func (db *DB) Move(id []byte, delta int64) error {
	if !validID(id) || delta == 0 || delta < -maxBook || delta > maxBook {
		return ErrInvalid
	}
	key := string(id)
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[key]
	if !ok {
		return ErrNotFound
	}
	if l.book+delta < 0 {
		return ErrStock
	}
	if l.book+delta > maxBook {
		return ErrInvalid
	}
	l.book += delta
	l.mv += delta
	return nil
}

func (db *DB) Book(id []byte) (int64, error) {
	if !validID(id) {
		return 0, ErrInvalid
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[string(id)]
	if !ok {
		return 0, ErrNotFound
	}
	return l.book, nil
}

func (db *DB) Price(id []byte) (int64, error) {
	if !validID(id) {
		return 0, ErrInvalid
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[string(id)]
	if !ok {
		return 0, ErrNotFound
	}
	return l.price, nil
}

func (db *DB) Moved(id []byte) (int64, error) {
	if !validID(id) {
		return 0, ErrInvalid
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[string(id)]
	if !ok {
		return 0, ErrNotFound
	}
	return l.mv, nil
}

func (db *DB) Tabs() int64 { return db.tabs }
func (db *DB) Tpct() int64 { return db.tpct }
func (db *DB) Lim() int64  { return db.lim }

// Tol 返回判定那一刻按当前账面数计算的容差。
func (db *DB) Tol(id []byte) (int64, error) {
	book, err := db.Book(id)
	if err != nil {
		return 0, err
	}
	tol := book * db.tpct / 100
	if db.tabs > tol {
		tol = db.tabs
	}
	return tol, nil
}

func (db *DB) Has(id []byte) bool {
	if !validID(id) {
		return false
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	_, ok := db.locs[string(id)]
	return ok
}

// View 返回库位当前账面数、累计净移动量与单价。
func (db *DB) View(id []byte) (book, mv, price int64, err error) {
	if !validID(id) {
		return 0, 0, 0, ErrInvalid
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[string(id)]
	if !ok {
		return 0, 0, 0, ErrNotFound
	}
	return l.book, l.mv, l.price, nil
}

// Lock/Unlock 暴露内部互斥量，供 count 在“count.mu -> db.mu”固定锁序下
// 把“读快照-判定-落账”合并为一个原子临界区；Move 只取 db.mu，锁序单向无死锁。
func (db *DB) Lock()   { db.mu.Lock() }
func (db *DB) Unlock() { db.mu.Unlock() }

// SnapshotLocked 同 View，但要求调用方已持有 Lock。
func (db *DB) SnapshotLocked(id []byte) (book, mv, price int64, err error) {
	if !validID(id) {
		return 0, 0, 0, ErrInvalid
	}
	l, ok := db.locs[string(id)]
	if !ok {
		return 0, 0, 0, ErrNotFound
	}
	return l.book, l.mv, l.price, nil
}

// SetCountedLocked 同 SetCounted，但要求调用方已持有 Lock。
func (db *DB) SetCountedLocked(id []byte, counted int64) error {
	if !validID(id) || counted < 0 || counted > maxBook {
		return ErrInvalid
	}
	l, ok := db.locs[string(id)]
	if !ok {
		return ErrNotFound
	}
	l.book = counted
	return nil
}

// ApplyDiffLocked 同 ApplyDiff，但要求调用方已持有 Lock。
func (db *DB) ApplyDiffLocked(id []byte, diff int64) error {
	if !validID(id) {
		return ErrInvalid
	}
	l, ok := db.locs[string(id)]
	if !ok {
		return ErrNotFound
	}
	if l.book+diff < 0 {
		return ErrStock
	}
	l.book += diff
	return nil
}

// ApplyDiff 按差值落账（审批通过），不触碰 mv；账面为负报库存不足。
func (db *DB) ApplyDiff(id []byte, diff int64) error {
	if !validID(id) {
		return ErrInvalid
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[string(id)]
	if !ok {
		return ErrNotFound
	}
	if l.book+diff < 0 {
		return ErrStock
	}
	l.book += diff
	return nil
}

// SetCounted 将账面数置为实盘数（容差内采纳），不触碰 mv。
func (db *DB) SetCounted(id []byte, counted int64) error {
	if !validID(id) || counted < 0 || counted > maxBook {
		return ErrInvalid
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	l, ok := db.locs[string(id)]
	if !ok {
		return ErrNotFound
	}
	l.book = counted
	return nil
}
