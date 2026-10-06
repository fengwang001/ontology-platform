package leasechain

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// applyToService 按操作日志在正式实现上重放，返回错误分类。
func applyToService(s *Service, op Op) error {
	switch op.Kind {
	case opMaster:
		_, err := s.CreateMaster(op.Now, op.A, op.B, op.X, op.Y, op.R)
		return err
	case opGrantGeneral:
		return s.GrantGeneral(op.Now, op.A, op.B)
	case opRevokeGeneral:
		return s.RevokeGeneral(op.Now, op.A, op.B)
	case opGrantOneTime:
		_, err := s.GrantOneTime(op.Now, op.A, op.B, op.I, op.X, op.Y, op.R)
		return err
	case opSublease:
		_, err := s.Sublease(op.Now, op.I, op.B, op.X, op.Y, op.R)
		return err
	case opRecognize:
		return s.Recognize(op.Now, op.I, op.A)
	case opTerminate:
		return s.Terminate(op.Now, op.I, op.A)
	case opExit:
		return s.Exit(op.Now, op.I, op.A)
	case opPay:
		_, err := s.PayArrear(op.Now, op.I, op.ID, op.R)
		return err
	case opAdvance:
		return s.Advance(op.Now)
	}
	return nil
}

func classify(err error) string {
	for _, target := range []error{
		ErrInvalidParam, ErrClockRollback, ErrLeaseUnavailable, ErrNoConsent,
		ErrTermOutOfRange, ErrRentTooHigh, ErrDepthExceeded, ErrIllegalState,
	} {
		if errors.Is(err, target) {
			return target.Error()
		}
	}
	if err == nil {
		return "OK"
	}
	return "OTHER:" + err.Error()
}

// TestRandomDifferential 生成随机操作序列，正式实现与朴素模型必须在错误分类
// 与全部状态上逐笔一致；-v 时打印每步输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	cfg := Config{P: 120, D: 3, G: 4, PayDay: 12}
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			s, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			m := NewNaive(cfg)
			ops := genOps(t, rng, 220)
			for idx, op := range ops {
				e1 := applyToService(s, op)
				e2 := m.Apply(op)
				t.Logf("seed=%d step=%d in=%s out1=%s out2=%s reason: %s",
					seed, idx, opString(op), classify(e1), classify(e2), decisionBasis(op))
				if classify(e1) != classify(e2) {
					t.Fatalf("seed=%d step=%d %s: service=%v naive=%v", seed, idx, opString(op), e1, e2)
				}
				got, want := s.snapshotState(), m.State()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("seed=%d step=%d state mismatch after %s\nservice=%+v\nnaive =%+v",
						seed, idx, opString(op), got, want)
				}
			}
		})
	}
}

func opString(op Op) string {
	return fmt.Sprintf("kind=%d now=%d id=%d i=%d a=%q b=%q x=%d y=%d r=%d",
		op.Kind, op.Now, op.ID, op.I, op.A, op.B, op.X, op.Y, op.R)
}

func decisionBasis(op Op) string {
	switch op.Kind {
	case opSublease:
		return "次序: 参数->时钟->租约存在->自身->同意->期限->租金->深度->已有下级"
	case opPay:
		return "次序: 参数->时钟->欠费/租约存在->连带资格->已清偿->超付"
	case opTerminate, opExit:
		return "次序: 参数->时钟->租约存在->身份/下级约束->级联"
	default:
		return "次序: 参数->时钟->状态约束"
	}
}
