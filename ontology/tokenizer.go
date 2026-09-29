package ontology

// 本文件实现 CDC 事件的确定性令牌化脱敏。

import (
	"fmt"
	"io"
	"log"
	"sort"
	"strconv"
	"sync"
)

// ColumnValue 是列值的合法载荷：nil 表示 SQL NULL，
// 其余为 string / int64 / float64 / bool / []byte 之一。
type ColumnValue any

// Image 是一个行镜像（变更前或变更后）：列名 -> 列值。
// nil 表示该镜像不存在（例如 insert 没有前镜像、delete 没有后镜像）。
type Image map[string]ColumnValue

// Event 是一条 CDC 变更事件。Before/After 任一可为 nil。
type Event struct {
	Table  string
	Before Image
	After  Image
}

// Domain 描述一个令牌域的配置。
type Domain struct {
	// Name 为域名，同时是令牌前缀，必须非空。
	Name string
	// TokenLimit 为该域最多可分配的不同原值个数；0 表示使用 DefaultTokenLimit。
	TokenLimit int
}

// ColumnRef 引用某张表的某个敏感列。
type ColumnRef struct {
	Table  string
	Column string
}

// Config 是脱敏器配置：列 -> 所属域。
type Config struct {
	Domains map[string]Domain
	Columns map[ColumnRef]string
}

// DefaultTokenLimit 是 Domain.TokenLimit 为 0 时的默认上限。
const DefaultTokenLimit = 1_000_000

// valueKey 是原值在域内的确定性身份键。nil（SQL NULL）不经过此类型。
type valueKey struct {
	kind byte // 's' string, 'i' int64, 'f' float64, 'b' bool, 'z' []byte
	text string
}

// Tokenizer 是并发安全的确定性脱敏器。
type Tokenizer struct {
	mu sync.Mutex

	domains map[string]Domain
	columns map[ColumnRef]string

	// tables 记录配置中出现过的全部表名，用于区分“未配置的表”。
	tables map[string]struct{}

	// tokens[domain][valueKey] = 该值在域内的编号（从 1 起连续）。
	tokens map[string]map[valueKey]int
	// next[domain] = 该域下一个待分配编号。
	next map[string]int

	logger *log.Logger
}

// Option 配置 New 的可选行为。
type Option func(*Tokenizer)

// WithLogger 将每步输入、令牌与判定依据写入 w（nil 表示丢弃日志）。
func WithLogger(w io.Writer) Option {
	return func(t *Tokenizer) {
		if w == nil {
			t.logger = log.New(io.Discard, "", 0)
		} else {
			t.logger = log.New(w, "[tokenize] ", log.LstdFlags|log.Lmicroseconds)
		}
	}
}

// New 校验配置并创建脱敏器。配置非法时返回 ReasonInvalidConfig，
// 且不会创建任何部分可用的状态。
func New(cfg Config, opts ...Option) (*Tokenizer, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	t := &Tokenizer{
		domains: make(map[string]Domain, len(cfg.Domains)),
		columns: make(map[ColumnRef]string, len(cfg.Columns)),
		tables:  make(map[string]struct{}),
		tokens:  make(map[string]map[valueKey]int),
		next:    make(map[string]int),
		logger:  log.New(io.Discard, "", 0),
	}
	for name, d := range cfg.Domains {
		if d.TokenLimit == 0 {
			d.TokenLimit = DefaultTokenLimit
		}
		t.domains[name] = d
		t.tokens[name] = make(map[valueKey]int)
		t.next[name] = 1
	}
	for ref, domain := range cfg.Columns {
		t.columns[ref] = domain
		t.tables[ref.Table] = struct{}{}
	}
	for _, opt := range opts {
		opt(t)
	}
	t.logger.Printf("init: domains=%d configured-columns=%d", len(t.domains), len(t.columns))
	return t, nil
}

func validateConfig(cfg Config) error {
	if len(cfg.Domains) == 0 {
		return rejectf(ReasonInvalidConfig, "at least one domain is required")
	}
	for name, d := range cfg.Domains {
		if name == "" {
			return rejectf(ReasonInvalidConfig, "domain map contains empty domain name")
		}
		if d.Name != name {
			return rejectf(ReasonInvalidConfig, "domain %q key does not match Name %q", name, d.Name)
		}
		if d.TokenLimit < 0 {
			return rejectf(ReasonInvalidConfig, "domain %q has negative token limit %d", name, d.TokenLimit)
		}
	}
	if len(cfg.Columns) == 0 {
		return rejectf(ReasonInvalidConfig, "at least one sensitive column is required")
	}
	for ref, domain := range cfg.Columns {
		if ref.Table == "" {
			return rejectf(ReasonInvalidConfig, "column binding has empty table name")
		}
		if ref.Column == "" {
			return rejectf(ReasonInvalidConfig, "column binding on table %q has empty column name", ref.Table)
		}
		if _, ok := cfg.Domains[domain]; !ok {
			return rejectf(ReasonInvalidConfig, "column %q on table %q binds to undefined domain %q", ref.Column, ref.Table, domain)
		}
	}
	return nil
}

// TokenCount 返回某域当前已分配的不同原值个数（供测试/可观测使用）。
func (t *Tokenizer) TokenCount(domain string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.next[domain] - 1
}

// NextNumber 返回某域下一编号（供测试/可观测使用）。
func (t *Tokenizer) NextNumber(domain string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, ok := t.next[domain]
	if !ok {
		return 0
	}
	return n
}

// Tokenize 对单条 CDC 事件做整事件原子脱敏，返回新建的事件；
// 调用方的入参事件不会被修改。任何拒绝都不会改变令牌表或各域下一编号。
func (t *Tokenizer) Tokenize(ev *Event) (*Event, error) {
	if ev == nil {
		return nil, rejectf(ReasonInvalidEvent, "event is nil")
	}
	if ev.Table == "" {
		return nil, rejectf(ReasonInvalidEvent, "event has empty table name")
	}

	// 先完整校验事件形状，校验阶段不触碰令牌状态。
	if err := validateImage(ev.Table, "before", ev.Before); err != nil {
		return nil, err
	}
	if err := validateImage(ev.Table, "after", ev.After); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := t.tables[ev.Table]; !ok {
		t.logger.Printf("reject: table=%q reason=%s detail=table has no sensitive columns configured", ev.Table, ReasonUnknownTable)
		return nil, rejectf(ReasonUnknownTable, "table %q has no configured sensitive columns", ev.Table)
	}

	t.logger.Printf("begin event table=%q before-present=%t after-present=%t", ev.Table, ev.Before != nil, ev.After != nil)

	// staged 记录本事件内新分配的 (域, 编号)，事件失败时按逆序撤销。
	type alloc struct {
		domain string
		key    valueKey
	}
	var staged []alloc

	rollback := func(why error) {
		for i := len(staged) - 1; i >= 0; i-- {
			a := staged[i]
			delete(t.tokens[a.domain], a.key)
			t.next[a.domain]--
		}
		t.logger.Printf("rollback: revoked=%d state-unchanged=true error=%v", len(staged), why)
	}

	// local 保证同一事件内同一原值在域内只占一个编号。
	local := make(map[string]map[valueKey]int)

	tokenFor := func(domain string, key valueKey) (int, error) {
		// 先查本事件暂存：同事件前后镜像/多列共享同一令牌表。
		if lm, ok := local[domain]; ok {
			if n, ok := lm[key]; ok {
				t.logger.Printf("reuse domain=%q key=%s token=%s basis=same-event", domain, key, makeToken(domain, n))
				return n, nil
			}
		}
		if n, ok := t.tokens[domain][key]; ok {
			t.logger.Printf("reuse domain=%q key=%s token=%s basis=prior-events", domain, key, makeToken(domain, n))
			return n, nil
		}

		n := t.next[domain]
		if n > t.domains[domain].TokenLimit {
			return 0, rejectf(ReasonTokenLimitExceeded, "domain %q token limit %d reached while assigning number %d", domain, t.domains[domain].TokenLimit, n)
		}
		t.tokens[domain][key] = n
		t.next[domain] = n + 1
		staged = append(staged, alloc{domain: domain, key: key})
		if local[domain] == nil {
			local[domain] = make(map[valueKey]int)
		}
		local[domain][key] = n
		t.logger.Printf("assign domain=%q key=%s token=%s basis=first-occurrence", domain, key, makeToken(domain, n))
		return n, nil
	}

	out := &Event{Table: ev.Table}

	process := func(which string, img Image) (Image, error) {
		if img == nil {
			return nil, nil
		}
		cols := make([]string, 0, len(img))
		for col := range img {
			cols = append(cols, col)
		}
		sort.Strings(cols) // 列名字节序

		res := make(Image, len(img))
		for _, col := range cols {
			v := img[col]
			ref := ColumnRef{Table: ev.Table, Column: col}
			domain, sensitive := t.columns[ref]
			if !sensitive {
				res[col] = v // 非敏感列原样透传
				t.logger.Printf("pass-through table=%q %s.%s value=%s", ev.Table, which, col, describeValue(v))
				continue
			}
			if v == nil {
				res[col] = nil // 空值保持空值，不分配令牌
				t.logger.Printf("null-preserve table=%q %s.%s domain=%q", ev.Table, which, col, domain)
				continue
			}
			key, err := makeValueKey(v)
			if err != nil {
				return nil, err
			}
			n, err := tokenFor(domain, key)
			if err != nil {
				return nil, err
			}
			tok := makeToken(domain, n)
			res[col] = tok
			t.logger.Printf("replace table=%q %s.%s domain=%q value=%s token=%q", ev.Table, which, col, domain, key, tok)
		}
		return res, nil
	}

	var err error
	if out.Before, err = process("before", ev.Before); err != nil {
		rollback(err)
		return nil, err
	}
	if out.After, err = process("after", ev.After); err != nil {
		rollback(err)
		return nil, err
	}

	t.logger.Printf("commit event table=%q new-assignments=%d", ev.Table, len(staged))
	return out, nil
}

func validateImage(table, which string, img Image) error {
	if img == nil {
		return nil
	}
	for col, v := range img {
		if col == "" {
			return rejectf(ReasonInvalidEvent, "table %q %s image has empty column name", table, which)
		}
		if v == nil {
			continue
		}
		if _, err := makeValueKey(v); err != nil {
			return rejectf(ReasonInvalidEvent, "table %q %s image column %q: %v", table, which, col, err)
		}
	}
	return nil
}

func makeValueKey(v ColumnValue) (valueKey, error) {
	switch x := v.(type) {
	case string:
		return valueKey{kind: 's', text: x}, nil
	case []byte:
		return valueKey{kind: 'z', text: string(x)}, nil
	case int64:
		return valueKey{kind: 'i', text: strconv.FormatInt(x, 10)}, nil
	case int:
		return valueKey{kind: 'i', text: strconv.Itoa(x)}, nil
	case float64:
		return valueKey{kind: 'f', text: strconv.FormatFloat(x, 'g', -1, 64)}, nil
	case float32:
		return valueKey{kind: 'f', text: strconv.FormatFloat(float64(x), 'g', -1, 32)}, nil
	case bool:
		return valueKey{kind: 'b', text: strconv.FormatBool(x)}, nil
	default:
		return valueKey{}, fmt.Errorf("unsupported column value type %T", v)
	}
}

func (k valueKey) String() string {
	switch k.kind {
	case 's':
		return "str(" + strconv.Quote(k.text) + ")"
	case 'z':
		return "bytes(" + strconv.Quote(k.text) + ")"
	case 'i':
		return "int(" + k.text + ")"
	case 'f':
		return "float(" + k.text + ")"
	case 'b':
		return "bool(" + k.text + ")"
	default:
		return "unknown"
	}
}

func describeValue(v ColumnValue) string {
	if v == nil {
		return "NULL"
	}
	k, err := makeValueKey(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return k.String()
}

func makeToken(domain string, n int) string {
	return domain + "-" + strconv.Itoa(n)
}
