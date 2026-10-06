package planningpoker

import (
	"errors"
	"sync"
)

// Role 为成员角色。
type Role int

const (
	RoleVoter Role = iota
	RoleObserver
)

// Phase 为议题生命周期阶段。
type Phase int

const (
	PhaseIdle     Phase = iota // 尚无进行中的议题
	PhaseVoting                // 投票阶段（票隐藏）
	PhaseRevealed              // 本轮已揭示，等待主持人 Revote 或议题已终局
	PhaseEnded                 // 议题结束
)

// ResultCategory 为一次揭示对应的结果类别。
type ResultCategory int

const (
	ResultNoValidVotes ResultCategory = iota
	ResultConsensus
	ResultConverged
	ResultDiverged
	ResultTerminalNoResult
	ResultForcedValue
)

// ErrAlreadyExists 表示成员已在会话内再次 Join。按“状态不允许”类处理。
var ErrAlreadyExists = errors.New("planningpoker: user already in session")

// Config 为创建会话时的不可变配置。
type Config struct {
	Cards        []int // 2~20 张互不相同的正整数，严格递增
	RoundLimit   int   // R：1~5
	RoundSeconds int   // T：1~86400
	AutoReveal   bool
}

// CardCount 为某张牌的得票数。
type CardCount struct {
	Card  Card
	Count int
}

// RevealResult 为一次揭示的完整结果。
type RevealResult struct {
	Round          int
	RevealedAt     int64
	Distribution   []CardCount
	NumericVotes   int
	Category       ResultCategory
	Value          int
	ValueIsMeaning bool
	Final          bool
}

// PeekView 为 Peek 返回的视图。
type PeekView struct {
	Phase      Phase
	Round      int
	VotedCount int
	VoterTotal int
	OwnVote    *Card // 揭示前仅返回本人那张；观察者或未投为 nil
	Outcome    *RevealResult
}

// Session 为单个团队估点投票会话。
type Session struct {
	mu sync.Mutex

	host        string
	deck        *Deck
	roundLimit  int
	roundLength int64
	autoReveal  bool

	// lastNow 为上一次“被接受”操作（含查询）的时间戳。
	// 被拒绝的操作不更新它；到期处理由通过参数/时钟检查的操作驱动。
	lastNow int64
	nextGen int

	phase      Phase
	members    map[string]*memberState
	round      int   // 当前轮号（1 起）；揭示后保留为已揭示轮号
	roundStart int64 // 当前轮开始时刻

	// 投票阶段的增量统计，保证 Vote/Unvote/自动揭示判定均为 O(1)。
	votersPresent int // 在室投票者总数
	votedVoters   int // 其中已投票（含特殊牌）的人数

	// slotCounts[k]：第 k 个槽位当前票数。槽位 0..n-1 为数值牌（按
	// 声明次序），n 与 n+1 为两张固定特殊牌。揭示分布仅遍历该数组，
	// 开销只与牌组大小有关。
	slotCounts []int

	// votedSet 为当前轮已投投票者席位（user -> 加入代次），写操作 O(1)：
	//   - 投票阶段：随 Vote/Unvote/离开/改角色增量维护；
	//   - 揭示时：直接“冻结”为揭示时刻快照，无需遍历成员，
	//     因而揭示开销只与牌组大小有关。
	// 揭示后该快照冻结不动；席位是否仍在室由 Peek 按代次 O(1) 判定。
	votedSet map[string]int

	// revealedPresent 为快照中目前仍在室且代次匹配的人数，增量 O(1)。
	revealedGen     map[string]int
	revealedPresent int

	lastResult *RevealResult
}

type memberState struct {
	role Role
	// voteSlot 为当前投票槽位；-1 表示未投。
	voteSlot int
	// gen 为该“席位”的加入代次；每次 Join 递增。用于区分揭示后
	// 同一用户名离开又重新加入的不同席位。
	gen int
}

// NewSession 校验配置并创建会话；host 为主持人。
func NewSession(host string, cfg Config, now int64) (*Session, error) {
	if host == "" {
		return nil, ErrInvalidArgument
	}
	if cfg.RoundLimit < 1 || cfg.RoundLimit > 5 {
		return nil, ErrInvalidArgument
	}
	if cfg.RoundSeconds < 1 || cfg.RoundSeconds > 86400 {
		return nil, ErrInvalidArgument
	}
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	deck, err := newDeck(cfg.Cards)
	if err != nil {
		return nil, err
	}
	return &Session{
		host:        host,
		deck:        deck,
		roundLimit:  cfg.RoundLimit,
		roundLength: int64(cfg.RoundSeconds),
		autoReveal:  cfg.AutoReveal,
		lastNow:     now,
		phase:       PhaseIdle,
		members:     make(map[string]*memberState),
		slotCounts:  make([]int, deck.numericCount()+len(specialCards)),
		votedSet:    make(map[string]int),
	}, nil
}
