package classify

import (
	"errors"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want Class
		err  error
	}{
		{"未知方法即非法即使InDialog", Message{"FOO", true, PriorityEmergency}, 0, ErrInvalidParam},
		{"空方法非法", Message{"", false, PriorityNormal}, 0, ErrInvalidParam},
		{"非法优先级", Message{"INVITE", false, Priority(9)}, 0, ErrInvalidParam},
		{"会话内OPTIONS为Exempt", Message{"OPTIONS", true, PriorityNormal}, ClassExempt, nil},
		{"会话内普通INVITE为Exempt", Message{"INVITE", true, PriorityNormal}, ClassExempt, nil},
		{"emergency的MESSAGE为Exempt", Message{"MESSAGE", false, PriorityEmergency}, ClassExempt, nil},
		{"emergency优先于non-urgent不可能", Message{"OPTIONS", false, PriorityEmergency}, ClassExempt, nil},
		{"ACK为Exempt", Message{"ACK", false, PriorityNormal}, ClassExempt, nil},
		{"BYE为Exempt", Message{"BYE", false, PriorityNonUrgent}, ClassExempt, nil},
		{"CANCEL为Exempt", Message{"CANCEL", false, PriorityNormal}, ClassExempt, nil},
		{"PRACK为Exempt", Message{"PRACK", false, PriorityNormal}, ClassExempt, nil},
		{"non-urgent的INVITE为Low", Message{"INVITE", false, PriorityNonUrgent}, ClassLow, nil},
		{"non-urgent的REGISTER为Low", Message{"REGISTER", false, PriorityNonUrgent}, ClassLow, nil},
		{"OPTIONS为Low", Message{"OPTIONS", false, PriorityNormal}, ClassLow, nil},
		{"SUBSCRIBE为Low", Message{"SUBSCRIBE", false, PriorityNormal}, ClassLow, nil},
		{"MESSAGE为Low", Message{"MESSAGE", false, PriorityNormal}, ClassLow, nil},
		{"INVITE为Normal", Message{"INVITE", false, PriorityNormal}, ClassNormal, nil},
		{"REGISTER为Normal", Message{"REGISTER", false, PriorityNormal}, ClassNormal, nil},
		{"UPDATE为Normal", Message{"UPDATE", false, PriorityNormal}, ClassNormal, nil},
		{"REFER为Normal", Message{"REFER", false, PriorityNormal}, ClassNormal, nil},
		{"NOTIFY为Normal", Message{"NOTIFY", false, PriorityNormal}, ClassNormal, nil},
		{"PUBLISH为Normal", Message{"PUBLISH", false, PriorityNormal}, ClassNormal, nil},
		{"INFO为Normal", Message{"INFO", false, PriorityNormal}, ClassNormal, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Classify(tc.msg)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err == nil && got != tc.want {
				t.Fatalf("class = %v, want %v", got, tc.want)
			}
			t.Logf("输入=%+v 输出=%v 错误=%v 判定依据=分级表先中先得", tc.msg, got, err)
		})
	}
}
