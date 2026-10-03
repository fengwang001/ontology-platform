package ontology

import (
	"sort"
	"sync"
)

var (
	ErrClock      = sentinelError("clock moved backwards")
	ErrPeerExists = sentinelError("peer already exists")
	ErrBanned     = sentinelError("peer is banned")
	ErrBadArg     = sentinelError("bad argument")
	ErrNoPeer     = sentinelError("peer does not exist")
	ErrNoRequest  = sentinelError("no inflight request")
)

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

type Config struct {
	Blocks              int
	BaseConcurrency     int
	GlobalInflightLimit int
	MaxBlockRequests    int
	Timeout             int64
	FailureBanThreshold int
}

type DoneResult struct {
	Banned   bool
	Canceled []string
}

type ExpiredRequest struct {
	Issued int64
	PeerID string
	Block  int
}

type BlockScheduler struct {
	mu                  sync.Mutex
	blocks              int
	baseConcurrency     int
	globalInflightLimit int
	maxBlockRequests    int
	timeout             int64
	failureBanThreshold int
	now                 int64
	peers               map[string]*peerState
	banned              map[string]bool
	completed           []bool
	blockInflight       []int
	globalInflight      int
}

type peerState struct {
	id       string
	have     []bool
	inflight map[int]int64
	failed   map[int]bool
	failures int
	timeouts int
}

func NewBlockScheduler(config Config) (*BlockScheduler, error) {
	if config.Blocks < 1 || config.Blocks > 4096 ||
		config.BaseConcurrency < 1 || config.BaseConcurrency > 16 ||
		config.GlobalInflightLimit < 1 ||
		config.MaxBlockRequests < 2 ||
		config.Timeout < 1 ||
		config.FailureBanThreshold < 1 {
		return nil, ErrBadArg
	}

	return &BlockScheduler{
		blocks:              config.Blocks,
		baseConcurrency:     config.BaseConcurrency,
		globalInflightLimit: config.GlobalInflightLimit,
		maxBlockRequests:    config.MaxBlockRequests,
		timeout:             config.Timeout,
		failureBanThreshold: config.FailureBanThreshold,
		peers:               make(map[string]*peerState),
		banned:              make(map[string]bool),
		completed:           make([]bool, config.Blocks),
		blockInflight:       make([]int, config.Blocks),
	}, nil
}

func (s *BlockScheduler) AddPeer(id string, have []bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" || len(have) != s.blocks {
		return ErrBadArg
	}
	if _, exists := s.peers[id]; exists {
		return ErrPeerExists
	}
	if s.banned[id] {
		return ErrBanned
	}

	s.peers[id] = &peerState{
		id:       id,
		have:     append([]bool(nil), have...),
		inflight: make(map[int]int64),
		failed:   make(map[int]bool),
	}
	return nil
}

func (s *BlockScheduler) Have(id string, block int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer := s.peers[id]
	if peer == nil {
		return ErrNoPeer
	}
	if block < 0 || block >= s.blocks {
		return ErrBadArg
	}

	peer.have[block] = true
	return nil
}

func (s *BlockScheduler) Drop(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	peer := s.peers[id]
	if peer == nil {
		return ErrNoPeer
	}

	for block := range peer.inflight {
		s.blockInflight[block]--
		s.globalInflight--
	}
	delete(s.peers, id)
	return nil
}

func (s *BlockScheduler) Next(now int64, id string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return 0, false, ErrClock
	}
	s.now = now

	peer := s.peers[id]
	if peer == nil {
		return 0, false, ErrNoPeer
	}
	if s.banned[id] {
		return 0, false, ErrBanned
	}

	capacity := s.peerCapacity(peer)
	if len(peer.inflight) >= capacity || s.globalInflight >= s.globalInflightLimit || s.completeLocked() {
		return 0, false, nil
	}

	hasFresh := false
	for block := 0; block < s.blocks; block++ {
		if !s.completed[block] && s.blockInflight[block] == 0 && s.availability(block) > 0 {
			hasFresh = true
			break
		}
	}

	candidate := -1
	candidateAvailable := 0
	candidateBlockInflight := 0

	for block := 0; block < s.blocks; block++ {
		if s.completed[block] || !peer.have[block] || peer.failed[block] {
			continue
		}

		if hasFresh {
			if s.blockInflight[block] != 0 {
				continue
			}
			available := s.availability(block)
			if available == 0 {
				continue
			}
			if candidate == -1 || available < candidateAvailable ||
				(available == candidateAvailable && block < candidate) {
				candidate = block
				candidateAvailable = available
			}
		} else {
			if _, requested := peer.inflight[block]; requested {
				continue
			}
			if s.blockInflight[block] >= s.maxBlockRequests {
				continue
			}
			available := s.availability(block)
			if candidate == -1 ||
				s.blockInflight[block] < candidateBlockInflight ||
				(s.blockInflight[block] == candidateBlockInflight && available < candidateAvailable) ||
				(s.blockInflight[block] == candidateBlockInflight && available == candidateAvailable && block < candidate) {
				candidate = block
				candidateAvailable = available
				candidateBlockInflight = s.blockInflight[block]
			}
		}
	}

	if candidate == -1 {
		return 0, false, nil
	}

	peer.inflight[candidate] = now
	s.blockInflight[candidate]++
	s.globalInflight++
	return candidate, true, nil
}

func (s *BlockScheduler) Done(now int64, id string, block int, ok bool) (DoneResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return DoneResult{}, ErrClock
	}
	s.now = now

	peer := s.peers[id]
	if peer == nil {
		return DoneResult{}, ErrNoPeer
	}
	if block < 0 || block >= s.blocks {
		return DoneResult{}, ErrBadArg
	}

	_, exists := peer.inflight[block]
	if !exists {
		return DoneResult{}, ErrNoRequest
	}

	delete(peer.inflight, block)
	s.blockInflight[block]--
	s.globalInflight--

	if ok {
		peer.timeouts = 0
		s.completed[block] = true

		canceled := make([]string, 0)
		for _, other := range s.peers {
			if other == peer {
				continue
			}
			if _, exists := other.inflight[block]; exists {
				delete(other.inflight, block)
				s.blockInflight[block]--
				s.globalInflight--
				canceled = append(canceled, other.id)
			}
		}
		sort.Strings(canceled)
		return DoneResult{Canceled: canceled}, nil
	}

	peer.failed[block] = true
	peer.failures++
	if peer.failures < s.failureBanThreshold {
		return DoneResult{}, nil
	}

	s.banned[id] = true
	for inflightBlock := range peer.inflight {
		s.blockInflight[inflightBlock]--
		s.globalInflight--
	}
	peer.inflight = make(map[int]int64)
	return DoneResult{Banned: true}, nil
}

func (s *BlockScheduler) Tick(now int64) ([]ExpiredRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return nil, ErrClock
	}
	s.now = now

	expired := make([]ExpiredRequest, 0)
	for _, peer := range s.peers {
		if s.banned[peer.id] {
			continue
		}
		for block, issued := range peer.inflight {
			if now-issued >= s.timeout {
				expired = append(expired, ExpiredRequest{
					Issued: issued,
					PeerID: peer.id,
					Block:  block,
				})
			}
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].Issued != expired[j].Issued {
			return expired[i].Issued < expired[j].Issued
		}
		if expired[i].PeerID != expired[j].PeerID {
			return expired[i].PeerID < expired[j].PeerID
		}
		return expired[i].Block < expired[j].Block
	})

	for _, request := range expired {
		peer := s.peers[request.PeerID]
		delete(peer.inflight, request.Block)
		s.blockInflight[request.Block]--
		s.globalInflight--
		peer.timeouts++
	}

	return expired, nil
}

func (s *BlockScheduler) Complete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeLocked()
}

func (s *BlockScheduler) peerCapacity(peer *peerState) int {
	capacity := s.baseConcurrency - peer.timeouts/2
	if capacity < 1 {
		return 1
	}
	return capacity
}

func (s *BlockScheduler) availability(block int) int {
	count := 0
	for id, peer := range s.peers {
		if !s.banned[id] && peer.have[block] {
			count++
		}
	}
	return count
}

func (s *BlockScheduler) completeLocked() bool {
	for _, isComplete := range s.completed {
		if !isComplete {
			return false
		}
	}
	return true
}
