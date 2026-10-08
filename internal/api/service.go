// Package api 提供对外读写接口，并把内部错误归一化为稳定的错误码。
package api

import (
	"errors"
	"strconv"
	"strings"

	"ontology/internal/instance"
)

// Service 是对外服务门面。
type Service struct {
	store *instance.Store
}

// New 创建服务。
func New() *Service {
	return &Service{store: instance.New()}
}

// NewWithStore 复用既有存储（便于测试与组合部署）。
func NewWithStore(st *instance.Store) *Service {
	return &Service{store: st}
}

// ErrorCode 是归一化后的拒绝原因码。
type ErrorCode string

const (
	CodeInvalidArgument   ErrorCode = "INVALID_ARGUMENT"    // 参数非法（最先判定）
	CodeConflict          ErrorCode = "OPTIMISTIC_CONFLICT" // 乐观并发凭证不匹配
	CodeBeforeEarliestBiz ErrorCode = "BEFORE_EARLIEST_BIZ" // 早于最早可追溯边界
)

// Error 是归一化错误。
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// PutResult 是写入结果。
type PutResult struct {
	SysVersion int64
}

// Put 创建或覆盖一个事实。baseToken 为凭证字符串："v<N>" 或 ""（等同 v0）。
func (svc *Service) Put(objType, pk string, bizStart int64, baseToken, payload string) (PutResult, error) {
	base, err := parseBaseToken(baseToken)
	if err != nil {
		return PutResult{}, err
	}
	v, err := svc.store.Write(instance.WriteRequest{
		ObjectType: objType,
		PrimaryKey: pk,
		BizStart:   bizStart,
		Base:       base,
		Deleted:    false,
		Payload:    payload,
	})
	if err != nil {
		return PutResult{}, normalize(err)
	}
	return PutResult{SysVersion: v.SysVersion}, nil
}

// Delete 逻辑删除一个事实（从 bizStart 起不再成立）。
func (svc *Service) Delete(objType, pk string, bizStart int64, baseToken string) (PutResult, error) {
	base, err := parseBaseToken(baseToken)
	if err != nil {
		return PutResult{}, err
	}
	v, err := svc.store.Write(instance.WriteRequest{
		ObjectType: objType,
		PrimaryKey: pk,
		BizStart:   bizStart,
		Base:       base,
		Deleted:    true,
	})
	if err != nil {
		return PutResult{}, normalize(err)
	}
	return PutResult{SysVersion: v.SysVersion}, nil
}

// Status 是双时态查询的结果标记。
type Status string

const (
	StatusNeverWritten Status = "NEVER_WRITTEN" // 主键从未写入，或查询系统时间早于首版本
	StatusDeleted      Status = "DELETED"       // 事实处于删除区间
	StatusAlive        Status = "ALIVE"         // 事实生效
)

// QueryResult 是双时态查询结果。
type QueryResult struct {
	Status     Status
	SysVersion int64
	BizStart   int64
	Payload    string
}

// Query 按业务时间与系统时间双坐标查询。
func (svc *Service) Query(objType, pk string, asOfSys, bizAt int64) (QueryResult, error) {
	if objType == "" || pk == "" || asOfSys < 0 || bizAt < 0 {
		return QueryResult{}, &Error{
			Code:    CodeInvalidArgument,
			Message: "object type, primary key and times must be valid",
		}
	}
	st, err := svc.store.GetAt(objType, pk, asOfSys, bizAt)
	if err != nil {
		return QueryResult{}, normalize(err)
	}
	switch st.Kind {
	case instance.StatePresent:
		return QueryResult{
			Status:     StatusAlive,
			SysVersion: st.Version.SysVersion,
			BizStart:   st.Version.BizStart,
			Payload:    st.Version.Payload,
		}, nil
	case instance.StateAbsent:
		return QueryResult{
			Status:     StatusDeleted,
			SysVersion: st.Version.SysVersion,
			BizStart:   st.Version.BizStart,
		}, nil
	default:
		return QueryResult{Status: StatusNeverWritten}, nil
	}
}

// HistorySegment 是历史时间轴上的一段。
type HistorySegment struct {
	Start      int64
	End        int64 // OpenEnd 时为 math.MaxInt64
	Status     Status
	SysVersion int64
	Payload    string
}

// History 重建系统时间 asOfSys 时主键可见的完整业务时间轴；
// ok=false 表示该主键在该系统时间从未写入。
func (svc *Service) History(objType, pk string, asOfSys int64) ([]HistorySegment, bool, error) {
	if objType == "" || pk == "" || asOfSys < 0 {
		return nil, false, &Error{Code: CodeInvalidArgument, Message: "invalid history argument"}
	}
	segs, ok := svc.store.HistoryAsOf(objType, pk, asOfSys)
	if !ok {
		return nil, false, nil
	}
	out := make([]HistorySegment, 0, len(segs))
	for _, seg := range segs {
		hs := HistorySegment{Start: seg.Start, End: seg.End, SysVersion: seg.State.Version.SysVersion}
		if seg.State.Kind == instance.StatePresent {
			hs.Status = StatusAlive
			hs.Payload = seg.State.Version.Payload
		} else {
			hs.Status = StatusDeleted
		}
		out = append(out, hs)
	}
	return out, true, nil
}

// LatestToken 返回当前最新版本凭证字符串（"v<N>"），供下次写入携带；
// 主键不存在时返回 "v0"。
func (svc *Service) LatestToken(objType, pk string) string {
	if v, ok := svc.store.Latest(objType, pk); ok {
		return "v" + strconv.FormatInt(v.SysVersion, 10)
	}
	return "v0"
}

// parseBaseToken 解析乐观并发凭证。
//
// 合法形式："" 与 "v0" 等价（新建语义），以及 "v<N>"（N>=1）。
// 前后空白、缺少 v 前缀、数字非法或为负都归为参数非法（凭证格式非法），
// 与“格式正确但与最新版本不一致”的冲突错误严格区分。
func parseBaseToken(token string) (int64, error) {
	t := strings.TrimSpace(token)
	if t == "" {
		return 0, nil
	}
	if t[0] != 'v' {
		return 0, &Error{Code: CodeInvalidArgument, Message: "base token must look like v<N>"}
	}
	n, err := strconv.ParseInt(t[1:], 10, 64)
	if err != nil || n < 0 {
		return 0, &Error{Code: CodeInvalidArgument, Message: "base token must look like v<N>"}
	}
	return n, nil
}

// normalize 把存储层错误一一映射为对外稳定错误码，不泄漏内部类型。
func normalize(err error) error {
	var se instance.StoreError
	if !errors.As(err, &se) {
		return &Error{Code: CodeInvalidArgument, Message: err.Error()}
	}
	switch {
	case errors.Is(err, instance.ErrInvalidArgument):
		return &Error{Code: CodeInvalidArgument, Message: se.Error()}
	case errors.Is(err, instance.ErrConflict):
		return &Error{Code: CodeConflict, Message: se.Error()}
	case errors.Is(err, instance.ErrBeforeEarliestBiz):
		return &Error{Code: CodeBeforeEarliestBiz, Message: se.Error()}
	default:
		return &Error{Code: CodeInvalidArgument, Message: se.Error()}
	}
}
