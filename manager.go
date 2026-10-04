// Package offline 实现流媒体离线下载许可管理器。
package offline

import (
	"errors"
	"sync"

	"ontology/device"
	"ontology/license"
	"ontology/playback"
)

// Manager 串行化管理设备、许可与播放判定。
type Manager struct {
	mu sync.Mutex

	devMgr *device.Manager
	licMgr *license.Manager

	accounts map[string]struct{}
	titles   map[string]int64
	lastNow  int64

	dmax int
	cool int64
	lr   int64
	lp   int64
	omax int
}

var _ = playback.StateNotPlayed

// NewManager 创建管理器。
func NewManager(dmax int, cool, lr, lp int64, omax int) *Manager {
	if dmax < 1 || dmax > 100 ||
		cool < 1 || cool > 1e9 ||
		lr < 1 || lr > 1e9 ||
		lp < 1 || lp > 1e9 ||
		omax < 1 || omax > 1e4 {
		panic(ErrInvalidArg)
	}
	return &Manager{
		devMgr:   device.New(dmax, cool),
		licMgr:   license.New(),
		accounts: make(map[string]struct{}),
		titles:   make(map[string]int64),
		dmax:     dmax,
		cool:     cool,
		lr:       lr,
		lp:       lp,
		omax:     omax,
	}
}

func validID(s string) bool { return s != "" }

func validNow(now int64) bool { return now >= 0 && now <= 1e12 }

// AddAccount 登记账号；重复报 ErrAccountExists。
func (m *Manager) AddAccount(now int64, acct string) error {
	if !validNow(now) || !validID(acct) {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.accounts[acct]; ok {
		return ErrAccountExists
	}
	m.accounts[acct] = struct{}{}
	m.devMgr.AddAccount(acct)
	m.licMgr.AddAccount(acct)
	m.lastNow = now
	return nil
}

// AddTitle 登记影片及其下架时刻 titleEnd（须 end > now）；重复报 ErrTitleExists。
func (m *Manager) AddTitle(now int64, title string, titleEnd int64) error {
	if !validNow(now) || !validID(title) || titleEnd <= now {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.titles[title]; ok {
		return ErrTitleExists
	}
	m.titles[title] = titleEnd
	m.lastNow = now
	return nil
}

// SetTitleEnd 调整下架时刻（可提前或延后，须 end > now）。
func (m *Manager) SetTitleEnd(now int64, title string, end int64) error {
	if !validNow(now) || !validID(title) || end <= now {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.titles[title]; !ok {
		return ErrTitleNotFound
	}
	m.titles[title] = end
	m.lastNow = now
	return nil
}

// Register 注册设备。
// 拒绝次序：参数非法 > 时钟回退 > 账号不存在 > 设备已注册 > 设备已满。
func (m *Manager) Register(now int64, acct, dev string) error {
	if !validNow(now) || !validID(acct) || !validID(dev) {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.accounts[acct]; !ok {
		return ErrAccountNotFound
	}
	if m.devMgr.Registered(acct, dev) {
		return ErrDeviceRegistered
	}
	if err := m.devMgr.Register(now, acct, dev); err != nil {
		if errors.Is(err, device.ErrFull) {
			return ErrDeviceFull
		}
		return err
	}
	m.lastNow = now
	return nil
}

// Deregister 注销设备：立即删除其全部离线许可，并留下一个冷却名额。
// 拒绝次序：参数非法 > 时钟回退 > 账号不存在 > 设备未注册。
func (m *Manager) Deregister(now int64, acct, dev string) error {
	if !validNow(now) || !validID(acct) || !validID(dev) {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.accounts[acct]; !ok {
		return ErrAccountNotFound
	}
	if !m.devMgr.Registered(acct, dev) {
		return ErrDeviceNotFound
	}
	m.licMgr.DeleteDevice(acct, dev)
	m.devMgr.Deregister(now, acct, dev)
	m.lastNow = now
	return nil
}

// Download 为（设备, 影片）签发或续期离线许可。
// 拒绝次序：参数非法 > 时钟回退 > 账号或影片不存在 > 设备未注册 >
// 已下架 > 已开播不可续 > 许可已满。
func (m *Manager) Download(now int64, acct, dev, title string) error {
	if !validNow(now) || !validID(acct) || !validID(dev) || !validID(title) {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.accounts[acct]; !ok {
		return ErrAccountNotFound
	}
	titleEnd, ok := m.titles[title]
	if !ok {
		return ErrTitleNotFound
	}
	if !m.devMgr.Registered(acct, dev) {
		return ErrDeviceNotFound
	}
	if now >= titleEnd {
		return ErrTitleOffShelf
	}

	rentalEnd := now + m.lr
	if rec, ok := m.licMgr.Get(acct, dev, title); ok {
		info := playback.Evaluate(now, rec.RentalEnd, rec.FirstPlay, m.lp, titleEnd)
		if info.State != playback.StateExpired {
			if rec.FirstPlay != 0 {
				return ErrAlreadyPlaying
			}
			// 未开播且未过期：续期，仅改 rentalEnd，不新占名额。
			m.licMgr.Renew(acct, dev, title, rentalEnd)
			m.lastNow = now
			return nil
		}
		// 已过期：作为新签发处理并替换旧记录（落到下方签发流程）。
	}

	valid := m.licMgr.CountValid(acct, now, m.lp, m.titleEndOf)
	if valid >= m.omax {
		return ErrLicenseFull
	}
	m.licMgr.Put(acct, dev, title, license.Record{RentalEnd: rentalEnd})
	m.lastNow = now
	return nil
}

// Play 尝试播放：许可有效则放行，首次放行写入 firstPlay = now（此后不可变）。
// 拒绝次序：参数非法 > 时钟回退 > 账号或影片不存在 > 设备未注册 > 无许可 > 已过期。
func (m *Manager) Play(now int64, acct, dev, title string) error {
	if !validNow(now) || !validID(acct) || !validID(dev) || !validID(title) {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return ErrClockRollback
	}
	if _, ok := m.accounts[acct]; !ok {
		return ErrAccountNotFound
	}
	titleEnd, ok := m.titles[title]
	if !ok {
		return ErrTitleNotFound
	}
	if !m.devMgr.Registered(acct, dev) {
		return ErrDeviceNotFound
	}
	rec, ok := m.licMgr.Get(acct, dev, title)
	if !ok {
		return ErrNoLicense
	}
	info := playback.Evaluate(now, rec.RentalEnd, rec.FirstPlay, m.lp, titleEnd)
	if info.State == playback.StateExpired {
		return &ExpiredError{Reason: info.Reason, Exp: info.Exp}
	}
	if rec.FirstPlay == 0 {
		// 首次放行才写入；后续 Play 不改变 firstPlay，也不延长任何期限。
		rec.FirstPlay = now
		m.licMgr.Put(acct, dev, title, rec)
	}
	m.lastNow = now
	return nil
}

// Status 只读查询许可阶段、过期时刻与原因；不推进时钟以外的任何状态。
func (m *Manager) Status(acct, dev, title string, now int64) (playback.Info, error) {
	if !validNow(now) || !validID(acct) || !validID(dev) || !validID(title) {
		return playback.Info{}, ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.lastNow {
		return playback.Info{}, ErrClockRollback
	}
	if _, ok := m.accounts[acct]; !ok {
		return playback.Info{}, ErrAccountNotFound
	}
	titleEnd, ok := m.titles[title]
	if !ok {
		return playback.Info{}, ErrTitleNotFound
	}
	if !m.devMgr.Registered(acct, dev) {
		return playback.Info{}, ErrDeviceNotFound
	}
	rec, ok := m.licMgr.Get(acct, dev, title)
	if !ok {
		return playback.Info{}, ErrNoLicense
	}
	return playback.Evaluate(now, rec.RentalEnd, rec.FirstPlay, m.lp, titleEnd), nil
}

func (m *Manager) titleEndOf(title string) int64 { return m.titles[title] }

// Touched 返回 (设备冷却触碰数, 许可记录触碰数)，用于复杂度验证。
func (m *Manager) Touched() (deviceTouched, licenseTouched int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devMgr.Touched(), m.licMgr.Touched()
}
