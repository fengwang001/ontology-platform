// Package segment 按画面组累积封片，维护全流连续的片序号与媒体时间线。
//
// 画面组依次累积进残片，累积时长不小于目标片长 D 即整片封出（恰等封片，
// 单个画面组超过 D 则独自成片），画面组永不拆分。片序号自 0 连续，
// 片的媒体起点等于此前全部已封片时长之和，与墙钟无关。
package segment

// Segment 是一片已封装的媒体，媒体区间为半开 [Start, Start+Dur)。
type Segment struct {
	Seq        int64 // 全流自 0 连续的片序号
	Start      int64 // 媒体时间起点（毫秒）
	Dur        int64 // 片时长（毫秒）
	Disc       bool  // 不连续标记：除全流第 0 片外各纪元的首片
	DiscBefore int64 // seq 小于本片的带标记片总数（含已淘汰片）
}

// End 返回片的媒体终点（半开区间右端）。
func (s Segment) End() int64 { return s.Start + s.Dur }

// Segmenter 是单条流的封片器。
type Segmenter struct {
	d            int64 // 目标片长
	nextSeq      int64 // 下一片序号
	mediaEnd     int64 // 已封片总时长，即下一片媒体起点
	partial      int64 // 残片已累积时长
	firstOfEpoch bool  // 下一片是否为当前纪元首片
	discCount    int64 // 已封出的带标记片总数
}

// NewSegmenter 构造目标片长为 d 的封片器，初始即处于首个纪元。
func NewSegmenter(d int64) *Segmenter {
	return &Segmenter{d: d, firstOfEpoch: true}
}

// BeginEpoch 在开新纪元（接管或重连）时调用；
// 须在旧纪元残片封完之后调用，使残片仍属旧纪元。
func (s *Segmenter) BeginEpoch() { s.firstOfEpoch = true }

// Push 累积一个画面组，残片达到 D 则封片并返回之，否则返回 nil。
func (s *Segmenter) Push(dur int64) *Segment {
	s.partial += dur
	if s.partial < s.d {
		return nil
	}
	seg := s.seal(s.partial)
	s.partial = 0
	return &seg
}

// SealPartial 在接管或断开时把非空残片原样封成一个短片；残片为空返回 nil。
func (s *Segmenter) SealPartial() *Segment {
	if s.partial == 0 {
		return nil
	}
	seg := s.seal(s.partial)
	s.partial = 0
	return &seg
}

// seal 封出时长为 dur 的一片，推进序号、时间线与标记计数。
func (s *Segmenter) seal(dur int64) Segment {
	seg := Segment{
		Seq:        s.nextSeq,
		Start:      s.mediaEnd,
		Dur:        dur,
		Disc:       s.firstOfEpoch && s.nextSeq != 0,
		DiscBefore: s.discCount,
	}
	if seg.Disc {
		s.discCount++
	}
	s.firstOfEpoch = false
	s.nextSeq++
	s.mediaEnd += dur
	return seg
}
