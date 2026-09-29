package featureflag

import (
	"context"
	"log/slog"
	"sync/atomic"
)

// snapshot 是某一版不可变规则集。一次求值连同其递归前置只看同一快照。
type snapshot struct {
	version int64
	rules   RuleSet
}

// Store 并发安全地保存当前生效的规则集版本。
type Store struct {
	current atomic.Pointer[snapshot]
	logger  *slog.Logger
}

// NewStore 创建空规则集的存储（版本 0，无任何开关）。
func NewStore(logger *slog.Logger) *Store {
	s := &Store{logger: logger}
	s.current.Store(&snapshot{version: 0, rules: RuleSet{}})
	return s
}

// Publish 校验并整体发布新规则集；被拒绝时当前生效版本不变。
// 发布成功后开始的求值必须看到新版本。
func (s *Store) Publish(rules RuleSet) (int64, error) {
	if err := validate(rules); err != nil {
		s.log().Warn("featureflag publish rejected",
			slog.Any("error", err),
		)
		return s.Version(), err
	}
	cp := cloneRules(rules)
	for {
		cur := s.current.Load()
		next := &snapshot{version: cur.version + 1, rules: cp}
		if s.current.CompareAndSwap(cur, next) {
			s.log().Info("featureflag publish accepted",
				slog.Int64("version", next.version),
				slog.Int("flags", len(cp)),
			)
			return next.version, nil
		}
	}
}

// Version 返回当前生效版本号。
func (s *Store) Version() int64 {
	return s.current.Load().version
}

// Eval 基于当前生效版本为用户求开关值；递归前置使用同一版本快照。
// ctx 仅用于携带日志上下文，求值本身不阻塞。
func (s *Store) Eval(ctx context.Context, flag string, userID string, attrs map[string]string) (*EvalResult, error) {
	snap := s.current.Load()
	ev := newEvaluator(snap, userID, attrs)

	// 记录整次求值（含递归前置）的判定依据。
	res, err := ev.eval(ctx, flag)
	if err != nil {
		s.log().Warn("featureflag eval",
			slog.Int64("version", snap.version),
			slog.String("input.flag", flag),
			slog.String("input.user_id", userID),
			slog.Any("input.attrs", attrs),
			slog.Any("error", err),
		)
		return nil, err
	}

	chain := make([]slog.Attr, 0, len(ev.memo))
	for name, r := range ev.memo {
		chain = append(chain, slog.Group(name,
			slog.String("variant", r.Variant),
			slog.String("reason", string(r.Reason)),
			slog.String("matched_rule", r.MatchedRule),
			slog.Int("bucket", r.Bucket),
		))
	}
	s.log().Info("featureflag eval",
		slog.Int64("version", res.Version),
		slog.String("input.flag", flag),
		slog.String("input.user_id", userID),
		slog.Any("input.attrs", attrs),
		slog.String("output.variant", res.Variant),
		slog.String("output.reason", string(res.Reason)),
		slog.String("output.matched_rule", res.MatchedRule),
		slog.Int("output.bucket", res.Bucket),
		slog.Attr{Key: "decisions", Value: slog.GroupValue(chain...)},
	)
	return res, nil
}

func (s *Store) log() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}
