package compensate_test

import (
	"context"
	"fmt"

	"ontology/compensate"
)

// kvEffect 是示例用补偿副作用：在独占标记键上做幂等条件插入，
// 并在对象的业务键上记录一次补偿效果。
type kvEffect struct{ index int }

func (e kvEffect) OwnedKey(ev compensate.Event, index int) string {
	return compensate.DefaultEffectKey(ev, index)
}

func (e kvEffect) Apply(_ context.Context, txn compensate.Txn, ev compensate.Event, index int) error {
	marker := e.OwnedKey(ev, index)
	if claimer, ok := txn.(compensate.EffectClaimer); ok {
		if !claimer.CreateIfAbsent(marker, []byte("1")) {
			return nil
		}
	}
	key := "obj/" + ev.Payload["object"] + "/compensated"
	txn.Put(key, []byte(fmt.Sprintf("effect-%d-by-%s", index, ev.EventID)))
	return nil
}

// 演示：正常消费一次、网络重试重复投递（跳过）、以及撤销先到（放弃补偿）。
func ExampleProcessor() {
	store := compensate.NewMemoryStore()
	reg := compensate.NewRegistry()
	reg.Register(compensate.CompensationSpec{
		ActionType: "transfer",
		Effects:    []compensate.Effect{kvEffect{}},
		TargetExists: func(txn compensate.Txn, ev compensate.Event) bool {
			_, err := txn.Get("obj/" + ev.Payload["object"] + "/alive")
			return err == nil
		},
	})
	p := compensate.NewProcessor(compensate.Config{Store: store, Specs: reg})

	_ = store.Update(func(txn compensate.Txn) error {
		txn.Put("obj/wallet/alive", []byte("1"))
		return nil
	})

	ev := compensate.Event{
		EventID:    "exec-1001",
		ActionType: "transfer",
		Payload:    map[string]string{"object": "wallet"},
	}

	r1 := p.Handle(context.Background(), ev)
	r2 := p.Handle(context.Background(), ev) // 网络重试：同一 EventID

	raw, _ := store.SnapshotGet("obj/wallet/compensated")
	fmt.Println("first:", r1.Outcome)
	fmt.Println("redelivery:", r2.Outcome)
	fmt.Println("effect:", string(raw))

	// 撤销先于另一条事件的首次投递：补偿被永久放弃。
	ev2 := compensate.Event{EventID: "exec-2002", ActionType: "transfer",
		Payload: map[string]string{"object": "wallet"}}
	fmt.Println("undo:", p.UndoArrived(ev2))
	fmt.Println("handle:", p.Handle(context.Background(), ev2).Outcome)

	// Output:
	// first: COMPLETED
	// redelivery: COMPLETED
	// effect: effect-0-by-exec-1001
	// undo: UNDO_WINS_ABANDONED
	// handle: SUPERSEDED
}
