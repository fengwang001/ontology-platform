// Package ingest 管理推流连接与接管纪元，并作为门面串联封片（segment）
// 与回看窗口（dvr）。
//
// 拒绝次序只报第一个：
//   - Connect：参数非法 > 时钟回退 > 流不存在 > 流占用
//   - Push/Disconnect：参数非法 > 时钟回退 > 流不存在 > 未连接 > 已被接管 > 纪元非法
//   - Playlist：参数非法 > 流不存在 > 尚未产生 > 已滑出窗口
//
// 被拒绝的操作不改任何状态（含全局时钟与最近活跃时刻）。
// 全局一把互斥锁，所有操作可线性化，等价于某个串行顺序。
package ingest

import (
	"errors"
	"sync"

	"ontology/dvr"
	"ontology/segment"
)

var (
	ErrInvalidParam    = errors.New("ingest: 参数非法")
	ErrClockRegression = errors.New("ingest: 时钟回退")
	ErrStreamNotFound  = errors.New("ingest: 流不存在")
	ErrStreamExists    = errors.New("ingest: 流已存在")
	ErrStreamBusy      = errors.New("ingest: 流占用")
	ErrNotConnected    = errors.New("ingest: 未连接")
	ErrTakenOver       = errors.New("ingest: 已被接管")
	ErrEpochInvalid    = errors.New("ingest: 纪元非法")
)

const (
	maxNow = int64(1_000_000_000_000)     // now 上界 1e12 毫秒
	maxTo  = int64(1_000_000_000_000_000) // Playlist 区间右端上界 1e15
)

// stream 是单条流的全部状态。
type stream struct {
	seg        *segment.Segmenter
	win        *dvr.Window
	epoch      int64 // 当前纪元（已接受的 Connect 次数）
	active     bool  // 是否有活动推流者
	lastActive int64 // 活动者最近一次被接受的 Push 或其 Connect 的 now
}

// Service 是推流接管与回看切片服务的门面。
type Service struct {
	mu      sync.Mutex
	d       int64 // 目标片长
	w       int64 // 回看窗口
	t       int64 // 静默阈值
	maxNow  int64 // 已接受操作的最大 now，初始 -1 使 now=0 合法
	streams map[string]*stream
}

// NewService 构造服务：D 为 1..60000 毫秒，W 与 T 为 1..1e9。
func NewService(d, w, t int64) (*Service, error) {
	if d < 1 || d > 60000 || w < 1 || w > 1_000_000_000 || t < 1 || t > 1_000_000_000 {
		return nil, ErrInvalidParam
	}
	return &Service{d: d, w: w, t: t, maxNow: -1, streams: make(map[string]*stream)}, nil
}

func validNowKey(now int64, key string) bool {
	return now >= 0 && now <= maxNow && key != ""
}

// CreateStream 建流，key 为非空字节串；重复建流报 ErrStreamExists。
func (s *Service) CreateStream(now int64, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNowKey(now, key) {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	if _, ok := s.streams[key]; ok {
		return ErrStreamExists
	}
	s.maxNow = now
	s.streams[key] = &stream{seg: segment.NewSegmenter(s.d), win: dvr.NewWindow(s.w)}
	return nil
}

// Connect 建立推流连接并返回新纪元。已有活动者时，force 为真或
// now 减去活动者最近活跃时刻不小于 T（恰等算静默）则接管，否则报 ErrStreamBusy。
func (s *Service) Connect(now int64, key string, force bool) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNowKey(now, key) {
		return 0, ErrInvalidParam
	}
	if now < s.maxNow {
		return 0, ErrClockRegression
	}
	st, ok := s.streams[key]
	if !ok {
		return 0, ErrStreamNotFound
	}
	if st.active && !force && now-st.lastActive < s.t {
		return 0, ErrStreamBusy
	}
	s.maxNow = now
	if st.active {
		s.sealPartial(st) // 接管：旧活动者残片原样封成短片，属旧纪元
	}
	st.epoch++
	st.active = true
	st.lastActive = now
	st.seg.BeginEpoch()
	return st.epoch, nil
}

// Disconnect 由活动者主动断开，流回到无活动者；残片非空则封成短片。
func (s *Service) Disconnect(now int64, key string, epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNowKey(now, key) {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	st, ok := s.streams[key]
	if !ok {
		return ErrStreamNotFound
	}
	if err := st.checkConn(epoch); err != nil {
		return err
	}
	s.maxNow = now
	s.sealPartial(st)
	st.active = false
	return nil
}

// Push 推入一个画面组，dur 为 1..60000 毫秒。
func (s *Service) Push(now int64, key string, epoch int64, dur int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validNowKey(now, key) || dur < 1 || dur > 60000 {
		return ErrInvalidParam
	}
	if now < s.maxNow {
		return ErrClockRegression
	}
	st, ok := s.streams[key]
	if !ok {
		return ErrStreamNotFound
	}
	if err := st.checkConn(epoch); err != nil {
		return err
	}
	s.maxNow = now
	st.lastActive = now
	if seg := st.seg.Push(dur); seg != nil {
		st.win.Add(*seg)
	}
	return nil
}

// checkConn 校验连接与纪元：未连接 > 已被接管 > 纪元非法。
func (st *stream) checkConn(epoch int64) error {
	if !st.active {
		return ErrNotConnected
	}
	if epoch < st.epoch {
		return ErrTakenOver
	}
	if epoch > st.epoch {
		return ErrEpochInvalid
	}
	return nil
}

// sealPartial 封出旧纪元残片（若有）并送入回看窗口。
func (s *Service) sealPartial(st *stream) {
	if seg := st.seg.SealPartial(); seg != nil {
		st.win.Add(*seg)
	}
}

// Playlist 只读查询媒体时间半开区间 [from, to) 的回看清单。
func (s *Service) Playlist(key string, from, to int64) (dvr.List, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" || from < 0 || from >= to || to > maxTo {
		return dvr.List{}, ErrInvalidParam
	}
	st, ok := s.streams[key]
	if !ok {
		return dvr.List{}, ErrStreamNotFound
	}
	return st.win.Playlist(from, to)
}
