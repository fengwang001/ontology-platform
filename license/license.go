// Package license 存储每账号的离线许可记录。
package license

// Key 标识一条（设备, 影片）许可。
type Key struct {
	Dev   string
	Title string
}

// Record 是一条离线许可记录。
type Record struct {
	RentalEnd int64
	FirstPlay int64 // 0 表示尚未开播
}

// Manager 保存全部账号的许可记录。
type Manager struct {
	accts   map[string]map[Key]Record
	touched int
}

// New 创建许可管理器。
func New() *Manager { return &Manager{accts: make(map[string]map[Key]Record)} }

// AddAccount 为账号建立许可存储槽。
func (m *Manager) AddAccount(acct string) {
	if _, ok := m.accts[acct]; !ok {
		m.accts[acct] = make(map[Key]Record)
	}
}

// Get 取出（设备, 影片）的许可记录；触碰恰好 1 条记录。
func (m *Manager) Get(acct, dev, title string) (Record, bool) {
	m.touched++
	r, ok := m.accts[acct][Key{Dev: dev, Title: title}]
	return r, ok
}

// Put 写入（覆盖）一条许可记录。
func (m *Manager) Put(acct, dev, title string, r Record) {
	m.accts[acct][Key{Dev: dev, Title: title}] = r
}

// Renew 仅更新已存在记录的租期，不改变 firstPlay。
func (m *Manager) Renew(acct, dev, title string, rentalEnd int64) {
	k := Key{Dev: dev, Title: title}
	r := m.accts[acct][k]
	r.RentalEnd = rentalEnd
	m.accts[acct][k] = r
}

// DeleteDevice 删除某设备在账号下的全部许可（注销时调用）。
func (m *Manager) DeleteDevice(acct, dev string) {
	recs := m.accts[acct]
	for k := range recs {
		if k.Dev == dev {
			delete(recs, k)
		}
	}
}

// CountValid 返回账号在 now、给定影片下架时刻表下有效（now < exp）的许可数。
// 只遍历该账号自身的许可记录，触碰数不超过本账号记录数；与其他账号无关。
func (m *Manager) CountValid(acct string, now, lp int64, titleEnd func(title string) int64) int {
	count := 0
	for k, r := range m.accts[acct] {
		m.touched++
		end := r.RentalEnd
		if r.FirstPlay != 0 {
			end = r.FirstPlay + lp
		}
		if te := titleEnd(k.Title); te < end {
			end = te
		}
		if now < end {
			count++
		}
	}
	return count
}

// Len 返回某账号的许可记录总数（测试/朴素对照用）。
func (m *Manager) Len(acct string) int { return len(m.accts[acct]) }

// Touched 返回累计触碰的许可记录数（每次 Get 计 1；CountValid 按遍历条数计）。
func (m *Manager) Touched() int { return m.touched }
