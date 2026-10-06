package collab

import "sync"

// Server is the offline collaboration sync server.
type Server struct {
	mu     sync.Mutex
	state  *state
	logger Logger
}

// NewServer creates an empty Server.
func NewServer() *Server {
	return &Server{state: newState()}
}
