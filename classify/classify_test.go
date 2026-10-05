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
		// 参数非法：未知方法（即使 InDialog 为真）、未知优先级。
		{"unknown method", Message{Method: "FOO", Priority: PriorityNormal}, 0, ErrInvalidParam},
		{"unknown method in dialog", Message{Method: "FOO", InDialog: true, Priority: PriorityEmergency}, 0, ErrInvalidParam},
		{"empty method", Message{Method: "", Priority: PriorityNormal}, 0, ErrInvalidParam},
		{"lowercase method", Message{Method: "invite", Priority: PriorityNormal}, 0, ErrInvalidParam},
		{"unknown priority", Message{Method: "INVITE", Priority: "high"}, 0, ErrInvalidParam},
		// Exempt：InDialog 优先于一切（会话内 OPTIONS 也是 Exempt）。
		{"in-dialog OPTIONS", Message{Method: "OPTIONS", InDialog: true, Priority: PriorityNonUrgent}, Exempt, nil},
		{"in-dialog INVITE", Message{Method: "INVITE", InDialog: true, Priority: PriorityNormal}, Exempt, nil},
		{"emergency REGISTER", Message{Method: "REGISTER", Priority: PriorityEmergency}, Exempt, nil},
		{"emergency MESSAGE", Message{Method: "MESSAGE", Priority: PriorityEmergency}, Exempt, nil},
		{"ACK", Message{Method: "ACK", Priority: PriorityNormal}, Exempt, nil},
		{"BYE", Message{Method: "BYE", Priority: PriorityNormal}, Exempt, nil},
		{"CANCEL", Message{Method: "CANCEL", Priority: PriorityNormal}, Exempt, nil},
		{"PRACK", Message{Method: "PRACK", Priority: PriorityNormal}, Exempt, nil},
		{"non-urgent ACK still exempt", Message{Method: "ACK", Priority: PriorityNonUrgent}, Exempt, nil},
		// Low：non-urgent 或 OPTIONS/SUBSCRIBE/MESSAGE。
		{"non-urgent INVITE", Message{Method: "INVITE", Priority: PriorityNonUrgent}, Low, nil},
		{"non-urgent REGISTER", Message{Method: "REGISTER", Priority: PriorityNonUrgent}, Low, nil},
		{"OPTIONS", Message{Method: "OPTIONS", Priority: PriorityNormal}, Low, nil},
		{"SUBSCRIBE", Message{Method: "SUBSCRIBE", Priority: PriorityNormal}, Low, nil},
		{"MESSAGE", Message{Method: "MESSAGE", Priority: PriorityNormal}, Low, nil},
		// Normal：其余七种。
		{"INVITE", Message{Method: "INVITE", Priority: PriorityNormal}, Normal, nil},
		{"REGISTER", Message{Method: "REGISTER", Priority: PriorityNormal}, Normal, nil},
		{"UPDATE", Message{Method: "UPDATE", Priority: PriorityNormal}, Normal, nil},
		{"REFER", Message{Method: "REFER", Priority: PriorityNormal}, Normal, nil},
		{"NOTIFY", Message{Method: "NOTIFY", Priority: PriorityNormal}, Normal, nil},
		{"PUBLISH", Message{Method: "PUBLISH", Priority: PriorityNormal}, Normal, nil},
		{"INFO", Message{Method: "INFO", Priority: PriorityNormal}, Normal, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Classify(tc.msg)
			if !errors.Is(err, tc.err) {
				t.Fatalf("Classify(%+v) err=%v, want %v", tc.msg, err, tc.err)
			}
			if tc.err == nil && got != tc.want {
				t.Fatalf("Classify(%+v)=%v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

func TestClassString(t *testing.T) {
	if Exempt.String() != "Exempt" || Low.String() != "Low" || Normal.String() != "Normal" {
		t.Fatalf("unexpected String: %v %v %v", Exempt, Low, Normal)
	}
}
