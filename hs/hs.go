// Package hs 维护半开连接表、已建立集合与 server 初始序号（serverISN）分配。
// 本包只提供存取与序号协商原语，握手事件判定在 handshake 包。
// Server 不是并发安全的，调用方必须自行串行化。
package hs

// Conn 是一条连接的序号协商结果。
type Conn struct {
	ClientISN int64
	ServerISN int64
}

// Server 是半开连接表与序号分配器。
type Server struct {
	half   map[string]Conn
	est    map[string]Conn
	next   int64
	probed int // 最近一次表查询检查过的半开连接个数；非导出，仅供包内测试核验 O(1)
}

// New 返回 nextISN=0 的空 Server。
func New() *Server {
	return &Server{half: make(map[string]Conn), est: make(map[string]Conn)}
}

// LookupHalf 按 src 查半开表，O(1) 映射查找。
func (s *Server) LookupHalf(src string) (Conn, bool) {
	s.probed = 1
	c, ok := s.half[src]
	return c, ok
}

// LookupEst 查已建立集合。
func (s *Server) LookupEst(src string) (Conn, bool) {
	c, ok := s.est[src]
	return c, ok
}

// Alloc 分配下一个 serverISN，单调递增、绝不重复。
func (s *Server) Alloc() int64 {
	isn := s.next
	s.next++
	return isn
}

// NextISN 返回下一个将分配的 serverISN（只读）。
func (s *Server) NextISN() int64 { return s.next }

// PutHalf 写入或替换半开连接。
func (s *Server) PutHalf(src string, c Conn) { s.half[src] = c }

// DeleteHalf 移除半开连接。
func (s *Server) DeleteHalf(src string) { delete(s.half, src) }

// PutEst 写入已建立集合。
func (s *Server) PutEst(src string, c Conn) { s.est[src] = c }
