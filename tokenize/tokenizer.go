// Package tokenize 提供变更数据捕获（CDC）事件的确定性令牌化脱敏。
package tokenize

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// Domain 配置一个令牌域：同域跨表共享令牌表。
type Domain struct {
	// MaxTokens 为该域允许的不同原值（即不同令牌）数量上限，必须 > 0。
	MaxTokens int
}

// Table 配置一张表的敏感列到域的映射。
type Table struct {
	// SensitiveColumns 将敏感列名映射到其所属域名。
	SensitiveColumns map[string]string
}

// Config 为脱敏器配置：域定义与表定义。
type Config struct {
	Domains map[string]Domain
	Tables  map[string]Table
}

// Event 为一条 CDC 事件。Before 为变更前镜像、After 为变更后镜像。
// 敏感列值仅允许 nil（空值）或 string；非敏感列原样透传。
type Event struct {
	Table  string
	Before map[string]any
	After  map[string]any
}

// Tokenizer 是并发安全的确定性令牌化脱敏器。
type Tokenizer struct {
	mu      sync.Mutex
	log     *slog.Logger
	domains map[string]*domainState
	tables  map[string]Table
}

type domainState struct {
	maxTokens int
	// tokens 为原值 -> 令牌 的已提交映射。
	tokens map[string]string
	// next 为下一个待分配编号（从 1 开始，连续无空洞）。
	next int
}

// New 依据配置构造脱敏器；配置非法时返回 KindInvalidConfig 错误。
func New(cfg Config, logger *slog.Logger) (*Tokenizer, error) {
	if len(cfg.Domains) == 0 {
		return nil, reject(KindInvalidConfig, "no domains configured")
	}
	if len(cfg.Tables) == 0 {
		return nil, reject(KindInvalidConfig, "no tables configured")
	}
	domains := make(map[string]*domainState, len(cfg.Domains))
	for name, d := range cfg.Domains {
		if name == "" {
			return nil, reject(KindInvalidConfig, "domain name must not be empty")
		}
		if _, dup := domains[name]; dup {
			return nil, reject(KindInvalidConfig, "duplicate domain %q", name)
		}
		if d.MaxTokens <= 0 {
			return nil, reject(KindInvalidConfig, "domain %q max_tokens must be positive, got %d", name, d.MaxTokens)
		}
		domains[name] = &domainState{maxTokens: d.MaxTokens, tokens: map[string]string{}, next: 1}
	}
	tables := make(map[string]Table, len(cfg.Tables))
	for name, tbl := range cfg.Tables {
		if name == "" {
			return nil, reject(KindInvalidConfig, "table name must not be empty")
		}
		cols := make(map[string]string, len(tbl.SensitiveColumns))
		for col, domain := range tbl.SensitiveColumns {
			if col == "" {
				return nil, reject(KindInvalidConfig, "table %q has empty sensitive column name", name)
			}
			if domain == "" {
				return nil, reject(KindInvalidConfig, "table %q column %q has empty domain", name, col)
			}
			if _, ok := domains[domain]; !ok {
				return nil, reject(KindInvalidConfig, "table %q column %q references undefined domain %q", name, col, domain)
			}
			cols[col] = domain
		}
		tables[name] = Table{SensitiveColumns: cols}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Tokenizer{log: logger, domains: domains, tables: tables}, nil
}

// Transform 对一条 CDC 事件整体脱敏：成功时返回脱敏后的新事件，
// 失败时整体拒绝且令牌表状态保持不变。
func (t *Tokenizer) Transform(ctx context.Context, ev Event) (Event, error) {
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.log.LogAttrs(ctx, slog.LevelInfo, "transform: event received",
		slog.String("table", ev.Table),
		slog.Bool("before_present", ev.Before != nil),
		slog.Bool("after_present", ev.After != nil),
	)

	tbl, ok := t.tables[ev.Table]
	if !ok {
		err := reject(KindUnknownTable, "table %q is not configured", ev.Table)
		t.log.LogAttrs(ctx, slog.LevelWarn, "transform: rejected", slog.String("reason", err.Error()))
		return Event{}, err
	}
	if ev.Before == nil && ev.After == nil {
		err := reject(KindInvalidEvent, "table %q event has neither before nor after image", ev.Table)
		t.log.LogAttrs(ctx, slog.LevelWarn, "transform: rejected", slog.String("reason", err.Error()))
		return Event{}, err
	}

	// staged 暂存本事件内各域新分配的令牌；只有整事件成功后才提交。
	type allocation struct {
		domain string
		value  string
		token  string
	}
	staged := map[string]map[string]string{}
	var allocations []allocation

	mask := func(phase string, image map[string]any) (map[string]any, error) {
		if image == nil {
			return nil, nil
		}
		columns := make([]string, 0, len(image))
		for col := range image {
			columns = append(columns, col)
		}
		sort.Strings(columns)
		out := make(map[string]any, len(image))
		for _, col := range columns {
			raw := image[col]
			domain, sensitive := tbl.SensitiveColumns[col]
			if !sensitive {
				out[col] = raw
				t.log.LogAttrs(ctx, slog.LevelDebug, "transform: passthrough non-sensitive",
					slog.String("phase", phase), slog.String("column", col))
				continue
			}
			if raw == nil {
				out[col] = nil
				t.log.LogAttrs(ctx, slog.LevelInfo, "transform: null kept null",
					slog.String("phase", phase), slog.String("column", col), slog.String("domain", domain),
					slog.String("basis", "null values never allocate tokens"))
				continue
			}
			value, ok := raw.(string)
			if !ok {
				return nil, reject(KindInvalidEvent,
					"table %q %s image sensitive column %q expects null or string, got %T", ev.Table, phase, col, raw)
			}
			ds := t.domains[domain]
			token, known := ds.tokens[value]
			basis := "token already committed in domain"
			if !known {
				if pending := staged[domain]; pending != nil {
					if tok, okPending := pending[value]; okPending {
						token, known = tok, true
						basis = "token allocated earlier within this event"
					}
				}
			}
			if !known {
				if ds.next-1+len(staged[domain]) >= ds.maxTokens {
					return nil, reject(KindTokenLimitExceeded,
						"domain %q token table full at %d tokens (max %d) while assigning value to table %q column %q",
						domain, ds.maxTokens, ds.maxTokens, ev.Table, col)
				}
				seq := ds.next + len(staged[domain])
				token = fmt.Sprintf("%s-%d", domain, seq)
				if staged[domain] == nil {
					staged[domain] = map[string]string{}
				}
				staged[domain][value] = token
				allocations = append(allocations, allocation{domain: domain, value: value, token: token})
				basis = fmt.Sprintf("first occurrence in domain, next sequence number %d", seq)
			}
			out[col] = token
			t.log.LogAttrs(ctx, slog.LevelInfo, "transform: value tokenized",
				slog.String("phase", phase), slog.String("column", col), slog.String("domain", domain),
				slog.String("input", value), slog.String("token", token), slog.String("basis", basis))
		}
		return out, nil
	}

	before, err := mask("before", ev.Before)
	if err != nil {
		t.log.LogAttrs(ctx, slog.LevelWarn, "transform: rejected; no state changed", slog.String("reason", err.Error()))
		return Event{}, err
	}
	after, err := mask("after", ev.After)
	if err != nil {
		t.log.LogAttrs(ctx, slog.LevelWarn, "transform: rejected; staged allocations revoked, no state changed",
			slog.Int("staged_allocations", len(allocations)), slog.String("reason", err.Error()))
		return Event{}, err
	}

	// 提交：本事件对令牌表的影响在此一次性原子可见。
	for _, a := range allocations {
		ds := t.domains[a.domain]
		ds.tokens[a.value] = a.token
		ds.next++
	}
	for _, a := range allocations {
		t.log.LogAttrs(ctx, slog.LevelInfo, "transform: committed token",
			slog.String("domain", a.domain), slog.String("token", a.token))
	}

	return Event{Table: ev.Table, Before: before, After: after}, nil
}

// DomainTokenCount 返回某域当前已分配的不同令牌数量（仅用于观测/测试）。
func (t *Tokenizer) DomainTokenCount(domain string) (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ds, ok := t.domains[domain]
	if !ok {
		return 0, false
	}
	return len(ds.tokens), true
}

// DomainTokens 返回某域当前 原值->令牌 映射的快照副本（仅用于观测/测试）。
func (t *Tokenizer) DomainTokens(domain string) (map[string]string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ds, ok := t.domains[domain]
	if !ok {
		return nil, false
	}
	snapshot := make(map[string]string, len(ds.tokens))
	for value, token := range ds.tokens {
		snapshot[value] = token
	}
	return snapshot, true
}
