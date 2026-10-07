package dtc

// FreezeFrame 冻结帧快照。
type FreezeFrame struct {
	Speed       int64
	CoolantTemp int64
	Odometer    int64
}

// freezeFrameSlot 全车唯一的冻结帧槽位。
type freezeFrameSlot struct {
	occupied bool
	owner    int // 占用者故障码编号
	frame    FreezeFrame
}

// tryCapture 尝试为 code 占用/更新槽位，severityOf 用于查询严重度。
// 规则：槽位空闲或占用者即本人则（重新）捕获；占用者为他人时仅当
// 新故障码严重度严格更大才替换；严重度相同保持原状。
func (s *freezeFrameSlot) tryCapture(code int, frame FreezeFrame, severityOf func(int) int) {
	if !s.occupied || s.owner == code {
		s.occupied = true
		s.owner = code
		s.frame = frame
		return
	}
	if severityOf(code) > severityOf(s.owner) {
		s.owner = code
		s.frame = frame
	}
}

// releaseIfOwned 占用者为 code 时释放槽位，不自动让给他人。
func (s *freezeFrameSlot) releaseIfOwned(code int) {
	if s.occupied && s.owner == code {
		s.occupied = false
		s.owner = 0
		s.frame = FreezeFrame{}
	}
}

func (s *freezeFrameSlot) releaseAll() {
	s.occupied = false
	s.owner = 0
	s.frame = FreezeFrame{}
}

// ownedBy 查询槽位是否被 code 占用。
func (s *freezeFrameSlot) ownedBy(code int) bool {
	return s.occupied && s.owner == code
}
