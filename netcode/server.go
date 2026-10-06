package netcode

import (
	"errors"
	"sort"
	"sync"
)

var ErrPlayerAlreadyRegistered = errors.New("player already registered")

type serverPlayer struct {
	position          int64
	receivedSequence  int64
	processedSequence int64
	active            bool
	pending           []Move
	receipts          map[int64]ReceiveResult
}

type Server struct {
	config   Config
	mu       sync.Mutex
	lastTick int64
	hasTick  bool
	active   []string
	players  map[string]*serverPlayer
}

func NewServer(config Config) (*Server, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	return &Server{
		config:  config,
		active:  make([]string, 0),
		players: make(map[string]*serverPlayer),
	}, nil
}

func (server *Server) RegisterPlayer(identifier string, position int64) error {
	if identifier == "" {
		return ErrInvalidConfig
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if _, exists := server.players[identifier]; exists {
		return ErrPlayerAlreadyRegistered
	}
	server.players[identifier] = &serverPlayer{
		position: clampPosition(position, server.config.WorldWidth),
		receipts: make(map[int64]ReceiveResult),
	}
	return nil
}

func (server *Server) Submit(playerIdentifier string, move Move) ReceiveResult {
	if playerIdentifier == "" || move.Sequence < 1 || move.Delta == 0 {
		return ReceiveResult{Reason: RejectionInvalidArgument}
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	player, exists := server.players[playerIdentifier]
	if !exists {
		return ReceiveResult{Reason: RejectionInvalidArgument}
	}
	if move.Sequence <= player.receivedSequence {
		previous := player.receipts[move.Sequence]
		copyOfPrevious := previous
		return ReceiveResult{
			Accepted: previous.Accepted,
			Reason:   previous.Reason,
			Existing: &copyOfPrevious,
		}
	}
	if move.Sequence > player.receivedSequence+1 {
		return ReceiveResult{Reason: RejectionGap}
	}
	if len(player.pending) >= server.config.Backlog {
		return ReceiveResult{Reason: RejectionBacklogFull}
	}
	player.receivedSequence++
	player.pending = append(player.pending, move)
	result := ReceiveResult{Accepted: true}
	player.receipts[move.Sequence] = result
	if !player.active {
		player.active = true
		server.active = append(server.active, playerIdentifier)
	}
	return result
}

func (server *Server) Tick(now int64) TickResult {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.hasTick && now <= server.lastTick {
		return TickResult{
			Now:           now,
			Accepted:      false,
			Reason:        RejectionClockRewound,
			Confirmations: map[string]Confirmation{},
			Players:       []string{},
		}
	}
	server.lastTick = now
	server.hasTick = true

	confirmations := make(map[string]Confirmation)
	changedIdentifiers := make([]string, 0)
	nextActive := make([]string, 0, len(server.active))
	for _, identifier := range server.active {
		player := server.players[identifier]
		if len(player.pending) == 0 {
			player.active = false
			continue
		}
		changedIdentifiers = append(changedIdentifiers, identifier)
		rejected := make(map[int64]struct{})
		count := server.config.Quota
		if count > len(player.pending) {
			count = len(player.pending)
		}
		for _, move := range player.pending[:count] {
			nextPosition, accepted := applyMove(
				player.position,
				move.Delta,
				server.config.WorldWidth,
				server.config.MaxStep,
			)
			player.position = nextPosition
			player.processedSequence = move.Sequence
			if !accepted {
				rejected[move.Sequence] = struct{}{}
			}
		}
		player.pending = player.pending[count:]
		confirmations[identifier] = Confirmation{
			ProcessedSequence: player.processedSequence,
			Position:          player.position,
			RejectedSequences: rejected,
		}
		if len(player.pending) > 0 {
			nextActive = append(nextActive, identifier)
		} else {
			player.active = false
		}
	}
	server.active = nextActive
	sort.Strings(changedIdentifiers)
	return TickResult{
		Now:           now,
		Accepted:      true,
		Confirmations: confirmations,
		Players:       changedIdentifiers,
	}
}

func (server *Server) Position(playerIdentifier string) (int64, bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	player, exists := server.players[playerIdentifier]
	if !exists {
		return 0, false
	}
	return player.position, true
}

func (server *Server) ProcessedSequence(playerIdentifier string) (int64, bool) {
	server.mu.Lock()
	defer server.mu.Unlock()
	player, exists := server.players[playerIdentifier]
	if !exists {
		return 0, false
	}
	return player.processedSequence, true
}
