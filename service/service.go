// Package service 对外提供读写接口，并把存储层的拒绝归一化为
// 可机器区分的错误码与查询状态标记。
package service

import (
	"fmt"
	"strconv"
	"strings"

	"ontology/store"
)

// Code 是归一化后的错误码。
type Code string

const (
	// CodeInvalidArgument 参数非法（主键为空、业务时间起点非法、凭证格式非法）。
	CodeInvalidArgument Code = "INVALID_ARGUMENT"
	// CodeConcurrencyConflict 乐观并发凭证不匹配。
	CodeConcurrencyConflict Code = "CONCURRENCY_CONFLICT"
	// CodeBizBoundary 业务时间起点早于最早可追溯边界。
	CodeBizBoundary Code = "BIZ_BOUNDARY_VIOLATION"
)

// Error 是归一化后的对外错误。
type Error struct {
	Code    Code
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Status 是对外查询结果标记。
type Status string

const (
	StatusFound           Status = "FOUND"
	StatusDeleted         Status = "DELETED"
	StatusNoVersionAtTime Status = "NO_VERSION_AT_TIME"
	StatusNeverWritten    Status = "NEVER_WRITTEN"
)

// QueryResult 是一次双时态查询的结果。
type QueryResult struct {
	Status  Status
	Version *store.Version // Status 为 FOUND 或 DELETED 时非空
}

// Service 是读写门面。
type Service struct {
	st *store.Store
}

// New 创建 Service。
func New(st *store.Store) *Service { return &Service{st: st} }

// parseToken 解析 "v<N>" 格式的并发凭证；格式非法归一化为 INVALID_ARGUMENT。
func parseToken(token string) (int64, *Error) {
	if !strings.HasPrefix(token, "v") {
		return 0, &Error{CodeInvalidArgument, fmt.Sprintf("并发凭证 %q 格式非法，应为 v<N>", token)}
	}
	n, err := strconv.ParseInt(token[1:], 10, 64)
	if err != nil || n < 0 {
		return 0, &Error{CodeInvalidArgument, fmt.Sprintf("并发凭证 %q 格式非法，应为 v<N>", token)}
	}
	return n, nil
}

// normalize 把存储层拒绝归一化为对外错误码。
func normalize(err error) *Error {
	if re, ok := err.(*store.RejectError); ok {
		switch re.Kind {
		case store.ErrInvalidArgument:
			return &Error{CodeInvalidArgument, re.Message}
		case store.ErrConcurrencyConflict:
			return &Error{CodeConcurrencyConflict, re.Message}
		case store.ErrBizBoundary:
			return &Error{CodeBizBoundary, re.Message}
		}
	}
	return &Error{CodeInvalidArgument, err.Error()}
}

// Write 普通写入。token 格式为 "v<N>"，"v0" 表示期望创建。
func (s *Service) Write(objectType, pk, token string, bizStart int64, payload string) (store.Version, *Error) {
	tok, e := parseToken(token)
	if e != nil {
		return store.Version{}, e
	}
	v, err := s.st.Write(objectType, pk, tok, bizStart, payload)
	if err != nil {
		return store.Version{}, normalize(err)
	}
	return v, nil
}

// Delete 逻辑删除。
func (s *Service) Delete(objectType, pk, token string, bizStart int64) (store.Version, *Error) {
	tok, e := parseToken(token)
	if e != nil {
		return store.Version{}, e
	}
	v, err := s.st.Delete(objectType, pk, tok, bizStart)
	if err != nil {
		return store.Version{}, normalize(err)
	}
	return v, nil
}

// Query 双时态查询。
func (s *Service) Query(objectType, pk string, sysQ, bizQ int64) QueryResult {
	v, st := s.st.Query(objectType, pk, sysQ, bizQ)
	switch st {
	case store.StatusFound:
		return QueryResult{Status: StatusFound, Version: &v}
	case store.StatusDeleted:
		return QueryResult{Status: StatusDeleted, Version: &v}
	case store.StatusNoVersionAtTime:
		return QueryResult{Status: StatusNoVersionAtTime}
	default:
		return QueryResult{Status: StatusNeverWritten}
	}
}

// LatestToken 返回主键当前最新凭证（"v<N>"），从未写入时为 "v0"。
func (s *Service) LatestToken(objectType, pk string) string {
	return "v" + strconv.FormatInt(s.st.LatestSeq(objectType, pk), 10)
}
