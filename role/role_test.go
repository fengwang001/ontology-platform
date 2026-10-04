package role_test

import (
	"errors"
	"testing"

	"ontology/mic"
	"ontology/role"
	"ontology/voice"
)

// 三个包围绕同一房间协作：role 创建并任免，voice 禁言，mic 麦序，
// 错误用 errors.Is 跨包识别。
func TestThreePackagesCollaborate(t *testing.T) {
	room := role.New(1)
	if err := role.Join(room, 0, 1); err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{2, 3, 4} {
		if err := role.Join(room, 0, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := role.SetRole(room, 0, 1, 2, role.Admin); err != nil {
		t.Fatal(err)
	}
	// mic 包操作麦序：3 上麦，4 排队。
	if err := mic.TakeMic(room, 0, 3); err != nil {
		t.Fatal(err)
	}
	if err := mic.TakeMic(room, 0, 4); err != nil {
		t.Fatal(err)
	}
	// voice 包禁言队列中的 4：保留队列位置。
	if err := voice.Mute(room, 10, 2, 4, 100); err != nil {
		t.Fatal(err)
	}
	// 3 下麦后补麦跳过 4，麦保持空。
	if err := mic.DropMic(room, 20, 3); err != nil {
		t.Fatal(err)
	}
	// 移交：Owner 1 -> 3。
	if err := role.Transfer(room, 30, 1, 3); err != nil {
		t.Fatal(err)
	}
	// 1 现在只是 Admin，无法禁言新 Owner 3。
	err := voice.Mute(room, 40, 1, 3, 80)
	if !errors.Is(err, role.ErrLowLevel) {
		t.Fatalf("跨包 errors.Is 等级不足，实际 %v", err)
	}
	// 同级 Admin 可解禁 4。
	if err := voice.Unmute(room, 50, 1, 4); err != nil {
		t.Fatalf("同级解禁: %v", err)
	}
	// 到期边界 100：新操作的入口补麦发现 4 禁言恰好解除，4 上麦。
	if err := role.Join(room, 100, 5); err != nil {
		t.Fatal(err)
	}
	// 重复/不在麦序等错误跨包 errors.Is。
	if err := mic.TakeMic(room, 110, 4); !errors.Is(err, mic.ErrDuplicate) {
		t.Fatalf("重复上麦: %v", err)
	}
	// Owner 3 仍有他人时离开须先移交。
	if err := role.Leave(room, 120, 3); !errors.Is(err, role.ErrMustTransfer) {
		t.Fatalf("Owner 离开: %v", err)
	}
}

func TestClockRewindAcrossPackages(t *testing.T) {
	room := mic.New(2)
	if err := mic.TakeMic(room, 0, 1); !errors.Is(err, mic.ErrNotInRoom) {
		t.Fatalf("空房操作者不在房: %v", err)
	}
	if err := role.Join(room, 10, 1); err != nil {
		t.Fatal(err)
	}
	if err := role.Join(room, 5, 2); !errors.Is(err, voice.ErrClockRewind) {
		t.Fatalf("时钟回退跨包识别: %v", err)
	}
}
