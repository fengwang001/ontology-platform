package abac

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	ErrPolicyIDRequired = errors.New("abac: policy id is required")
	ErrPolicyExists     = errors.New("abac: policy id already registered")
	ErrInvalidEffect    = errors.New("abac: policy effect must be allow or deny")
	ErrInvalidOp        = errors.New("abac: invalid condition operator")
	ErrInvalidAction    = errors.New("abac: action is required")
)

// Engine 是并发安全的 ABAC 判定引擎。
// 策略集合只能通过 Register 变更；Evaluate 只读，永远不会修改策略。
type Engine struct {
	mu       sync.RWMutex
	policies []Policy
	logger   Logger
}

// Logger 是判定审计日志接口。nil 表示不输出日志。
type Logger interface {
	LogDecision(ctx context.Context, entry LogEntry)
}

// LogEntry 是一条判定审计日志。
type LogEntry struct {
	Subject       Attributes
	Resource      Attributes
	Action        string
	Allowed       bool
	Reason        Reason
	DenyPolicy    string
	AllowPolicy   string
	Matched       []string
	Indeterminate []string
	Basis         string
}

// Option 配置引擎。
type Option func(*Engine)

// WithLogger 设置审计日志器。
func WithLogger(l Logger) Option {
	return func(e *Engine) { e.logger = l }
}

// NewEngine 创建空引擎。
func NewEngine(opts ...Option) *Engine {
	e := &Engine{}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Register 注册一条策略。策略 ID 必须非空且唯一。
func (e *Engine) Register(p Policy) error {
	if strings.TrimSpace(p.ID) == "" {
		return ErrPolicyIDRequired
	}
	if p.Effect != EffectAllow && p.Effect != EffectDeny {
		return ErrInvalidEffect
	}
	if err := validateConditions(p.Subject); err != nil {
		return err
	}
	if err := validateConditions(p.Resource); err != nil {
		return err
	}
	if err := validateConditions(p.Environment); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, existing := range e.policies {
		if existing.ID == p.ID {
			return ErrPolicyExists
		}
	}
	e.policies = append(e.policies, p)
	return nil
}

func validateConditions(conds []Condition) error {
	for _, c := range conds {
		if !validOps[c.Op] {
			return ErrInvalidOp
		}
	}
	return nil
}

var validOps = map[Op]bool{
	OpEqual: true, OpNotEqual: true, OpExists: true, OpNotExists: true,
	OpStringContains: true, OpStringPrefix: true, OpStringSuffix: true,
	OpLess: true, OpLessEqual: true, OpGreater: true, OpGreaterEqual: true,
	OpIn: true,
}

// Policies 返回当前已注册策略的快照，按 ID 字典序排列。
func (e *Engine) Policies() []Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Policy, len(e.policies))
	copy(out, e.policies)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Evaluate 执行一次判定，可被并发调用，且不修改引擎与策略状态。
func (e *Engine) Evaluate(ctx context.Context, req Request) (Decision, AuditDecision) {
	audit := AuditDecision{ResourcePresent: req.Resource != nil}
	if strings.TrimSpace(req.Action) == "" {
		audit.Reason = ReasonInvalidRequest
		return e.finish(ctx, req, audit)
	}

	snapshot := e.Policies()

	var matched, indeterminate []string
	var denyHits, allowHits []string
	for _, p := range snapshot {
		switch evaluatePolicy(p, req) {
		case resMatch:
			matched = append(matched, p.ID)
			if p.Effect == EffectDeny {
				denyHits = append(denyHits, p.ID)
			} else {
				allowHits = append(allowHits, p.ID)
			}
		case resIndeterminate:
			indeterminate = append(indeterminate, p.ID)
		}
	}

	audit.Matched = matched
	audit.Indeterminate = indeterminate

	// deny-overrides：只要存在命中的 deny，结果即拒绝，无论 allow 是否覆盖。
	if len(denyHits) > 0 {
		audit.Reason = ReasonDenyByPolicy
		audit.DenyPolicy = denyHits[0]
		if len(allowHits) > 0 {
			audit.AllowPolicy = allowHits[0]
		}
		return e.finish(ctx, req, audit)
	}

	// 有策略因引用属性缺失而无法求值时，不能把缺失当假值放行。
	if len(indeterminate) > 0 {
		audit.Reason = ReasonMissingAttr
		return e.finish(ctx, req, audit)
	}

	// 至少一条 allow 命中且无 deny 命中才允许。
	if len(allowHits) > 0 {
		audit.AllowPolicy = allowHits[0]
		audit.Decision = Decision{Allowed: true, Action: req.Action, Message: "allowed"}
		e.writeLog(ctx, req, audit)
		return audit.Decision, audit
	}

	// 默认拒绝：资源不存在与资源存在但无策略适用，对外完全一致。
	audit.Reason = ReasonNoApplicable
	return e.finish(ctx, req, audit)
}

// finish 统一构造对外拒绝：固定文案，不含原因、策略与资源存在性。
func (e *Engine) finish(ctx context.Context, req Request, audit AuditDecision) (Decision, AuditDecision) {
	audit.Decision = Decision{Allowed: false, Action: req.Action, Message: PublicDenyMessage}
	e.writeLog(ctx, req, audit)
	return audit.Decision, audit
}

func (e *Engine) writeLog(ctx context.Context, req Request, audit AuditDecision) {
	if e.logger == nil {
		return
	}
	e.logger.LogDecision(ctx, LogEntry{
		Subject:       req.Subject,
		Resource:      req.Resource,
		Action:        req.Action,
		Allowed:       audit.Allowed,
		Reason:        audit.Reason,
		DenyPolicy:    audit.DenyPolicy,
		AllowPolicy:   audit.AllowPolicy,
		Matched:       audit.Matched,
		Indeterminate: audit.Indeterminate,
		Basis:         auditBasis(audit),
	})
}

func auditBasis(a AuditDecision) string {
	switch a.Reason {
	case ReasonDenyByPolicy:
		if a.AllowPolicy != "" {
			return "deny-overrides: deny policy " + a.DenyPolicy +
				" overrides allow policy " + a.AllowPolicy
		}
		return "deny-overrides: matched deny policy " + a.DenyPolicy
	case ReasonMissingAttr:
		return "policy referenced a missing attribute: " + strings.Join(a.Indeterminate, ",")
	case ReasonInvalidRequest:
		return "invalid request"
	default:
		return "no applicable allow policy; default deny"
	}
}
