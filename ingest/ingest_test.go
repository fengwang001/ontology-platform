package ingest_test

import (
	"errors"
	"testing"

	"ontology/dvr"
	"ontology/ingest"
)

func mustService(t *testing.T, d, w, ms int64) *ingest.Service {
	t.Helper()
	s, err := ingest.NewService(d, w, ms)
	if err != nil {
		t.Fatalf("NewService(%d,%d,%d) 意外失败：%v", d, w, ms, err)
	}
	return s
}

func mustConnect(t *testing.T, s *ingest.Service, now int64, key string, force bool, wantEpoch int64) {
	t.Helper()
	epoch, err := s.Connect(now, key, force)
	if err != nil || epoch != wantEpoch {
		t.Fatalf("Connect(%d,%s,%v) = (%d,%v)，期望纪元 %d", now, key, force, epoch, err, wantEpoch)
	}
}

func mustPush(t *testing.T, s *ingest.Service, now int64, key string, epoch, dur int64) {
	t.Helper()
	if err := s.Push(now, key, epoch, dur); err != nil {
		t.Fatalf("Push(%d,%s,%d,%d) 意外失败：%v", now, key, epoch, dur, err)
	}
}

func wantErr(t *testing.T, got, want error, what string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s 错误为 %v，期望 %v", what, got, want)
	}
}

func checkPlaylist(t *testing.T, s *ingest.Service, key string, from, to int64,
	wantSeqs []int64, wantMS, wantDS int64) {
	t.Helper()
	got, err := s.Playlist(key, from, to)
	if err != nil {
		t.Fatalf("Playlist(%s,%d,%d) 意外失败：%v", key, from, to, err)
	}
	var seqs []int64
	for _, sg := range got.Segments {
		seqs = append(seqs, sg.Seq)
	}
	if len(seqs) != len(wantSeqs) {
		t.Fatalf("Playlist(%s,%d,%d) 返回 %v，期望 %v", key, from, to, seqs, wantSeqs)
	}
	for i := range seqs {
		if seqs[i] != wantSeqs[i] {
			t.Fatalf("Playlist(%s,%d,%d) 返回 %v，期望 %v", key, from, to, seqs, wantSeqs)
		}
	}
	if got.MediaSequence != wantMS || got.DiscontinuitySequence != wantDS {
		t.Fatalf("Playlist(%s,%d,%d) MS=%d DS=%d，期望 MS=%d DS=%d",
			key, from, to, got.MediaSequence, got.DiscontinuitySequence, wantMS, wantDS)
	}
	t.Logf("Playlist(%s,%d,%d) -> seqs=%v MS=%d DS=%d", key, from, to, seqs, wantMS, wantDS)
}

// TestWorkedExample 完整走查题目示例（D=4000，W=10000，T=5000）。
// 题目示例中"7−3=4 小于 T""8−3=5 恰等 T"为秒级简写，
// 与 T=5000 毫秒自洽的时间轴为 7000−3000=4000<5000、8000−3000=5000=T。
func TestWorkedExample(t *testing.T) {
	s := mustService(t, 4000, 10000, 5000)
	if err := s.CreateStream(0, "k"); err != nil {
		t.Fatalf("CreateStream 失败：%v", err)
	}
	mustConnect(t, s, 0, "k", false, 1)

	// 累积恰等 D 封出 seq0 [0,4000)，第 0 片不带标记。
	mustPush(t, s, 1000, "k", 1, 2000)
	mustPush(t, s, 2000, "k", 1, 2000)
	mustPush(t, s, 3000, "k", 1, 3000) // 留作残片

	// 7000-3000=4000 < T 报流占用；8000-3000=5000 恰等 T 接管。
	if _, err := s.Connect(7000, "k", false); !errors.Is(err, ingest.ErrStreamBusy) {
		t.Fatalf("Connect(7000) 应报流占用，得到 %v", err)
	}
	mustConnect(t, s, 8000, "k", false, 2) // 残片封为 seq1 [4000,7000)，属旧纪元不带标记

	// 旧纪元 Push 报已被接管。
	wantErr(t, s.Push(9000, "k", 1, 1000), ingest.ErrTakenOver, "旧纪元 Push")

	// 纪元 2 首片 seq2 [7000,12000) 带不连续标记。
	mustPush(t, s, 9000, "k", 2, 5000)
	mustPush(t, s, 10000, "k", 2, 4000) // seq3 [12000,16000)，淘汰 seq0

	checkPlaylist(t, s, "k", 5000, 13000, []int64{1, 2, 3}, 1, 0)
	checkPlaylist(t, s, "k", 7000, 8000, []int64{2}, 2, 0)   // 首片自身标记不计
	checkPlaylist(t, s, "k", 12000, 20000, []int64{3}, 3, 1) // to 截到 16000
	checkPlaylist(t, s, "k", 6000, 7000, []int64{1}, 1, 0)   // 端点落在片边界

	_, err := s.Playlist("k", 3999, 5000)
	wantErr(t, err, dvr.ErrSlidOut, "from<lo")
	_, err = s.Playlist("k", 16000, 17000)
	wantErr(t, err, dvr.ErrNotYetProduced, "from>=hi")

	// 校验 seq2 确实带标记、seq1 不带。
	got, _ := s.Playlist("k", 4000, 16000)
	if got.Segments[0].Disc || !got.Segments[1].Disc || got.Segments[2].Disc {
		t.Fatalf("标记不符：seq1=%v seq2=%v seq3=%v",
			got.Segments[0].Disc, got.Segments[1].Disc, got.Segments[2].Disc)
	}
}

// TestRejectOrder 覆盖各操作的拒绝次序，只报第一个错误。
func TestRejectOrder(t *testing.T) {
	newSvc := func(t *testing.T) *ingest.Service {
		s := mustService(t, 4000, 10000, 5000)
		if err := s.CreateStream(0, "a"); err != nil {
			t.Fatalf("建流失败：%v", err)
		}
		return s
	}
	t.Run("Connect", func(t *testing.T) {
		s := newSvc(t)
		mustConnect(t, s, 100, "a", false, 1)
		// 参数非法优先于时钟回退
		_, err := s.Connect(-1, "a", false)
		wantErr(t, err, ingest.ErrInvalidParam, "Connect now<0")
		_, err = s.Connect(50, "", false)
		wantErr(t, err, ingest.ErrInvalidParam, "Connect 空key")
		// 时钟回退优先于流不存在
		_, err = s.Connect(50, "ghost", false)
		wantErr(t, err, ingest.ErrClockRegression, "Connect 回退")
		// 流不存在
		_, err = s.Connect(200, "ghost", false)
		wantErr(t, err, ingest.ErrStreamNotFound, "Connect 不存在")
		// 流占用
		_, err = s.Connect(200, "a", false)
		wantErr(t, err, ingest.ErrStreamBusy, "Connect 占用")
	})
	t.Run("Push与Disconnect", func(t *testing.T) {
		s := newSvc(t)
		mustConnect(t, s, 100, "a", false, 1)
		mustPush(t, s, 200, "a", 1, 1000)
		// 参数非法优先于时钟回退
		wantErr(t, s.Push(50, "a", 1, 0), ingest.ErrInvalidParam, "Push dur=0")
		wantErr(t, s.Push(50, "a", 1, 60001), ingest.ErrInvalidParam, "Push dur超界")
		wantErr(t, s.Disconnect(-1, "a", 1), ingest.ErrInvalidParam, "Disconnect now<0")
		// 时钟回退优先于流不存在
		wantErr(t, s.Push(50, "ghost", 1, 1000), ingest.ErrClockRegression, "Push 回退")
		wantErr(t, s.Disconnect(50, "ghost", 1), ingest.ErrClockRegression, "Disconnect 回退")
		// 流不存在
		wantErr(t, s.Push(300, "ghost", 1, 1000), ingest.ErrStreamNotFound, "Push 不存在")
		wantErr(t, s.Disconnect(300, "ghost", 1), ingest.ErrStreamNotFound, "Disconnect 不存在")
		// 未连接优先于纪元错误
		wantErr(t, s.Push(300, "a", 99, 1000), ingest.ErrEpochInvalid, "Push 纪元过大")
		if err := s.Disconnect(300, "a", 1); err != nil {
			t.Fatalf("Disconnect 失败：%v", err)
		}
		wantErr(t, s.Push(400, "a", 99, 1000), ingest.ErrNotConnected, "Push 未连接优先")
		wantErr(t, s.Disconnect(400, "a", 1), ingest.ErrNotConnected, "Disconnect 未连接")
		// 已被接管：重连后旧纪元操作
		mustConnect(t, s, 500, "a", false, 2)
		wantErr(t, s.Push(600, "a", 1, 1000), ingest.ErrTakenOver, "Push 旧纪元")
		wantErr(t, s.Disconnect(600, "a", 1), ingest.ErrTakenOver, "Disconnect 旧纪元")
		wantErr(t, s.Push(600, "a", 3, 1000), ingest.ErrEpochInvalid, "Push 纪元超前")
		wantErr(t, s.Disconnect(600, "a", 3), ingest.ErrEpochInvalid, "Disconnect 纪元超前")
	})
	t.Run("Playlist", func(t *testing.T) {
		s := newSvc(t)
		// 参数非法优先于流不存在
		_, err := s.Playlist("ghost", 10, 10)
		wantErr(t, err, ingest.ErrInvalidParam, "Playlist from>=to")
		_, err = s.Playlist("ghost", -1, 10)
		wantErr(t, err, ingest.ErrInvalidParam, "Playlist from<0")
		_, err = s.Playlist("ghost", 0, 1_000_000_000_000_001)
		wantErr(t, err, ingest.ErrInvalidParam, "Playlist to超界")
		// 流不存在
		_, err = s.Playlist("ghost", 0, 10)
		wantErr(t, err, ingest.ErrStreamNotFound, "Playlist 不存在")
		// 尚未产生（无任何已封片）
		_, err = s.Playlist("a", 0, 10)
		wantErr(t, err, dvr.ErrNotYetProduced, "Playlist 空流")
	})
	t.Run("CreateStream重复与构造参数", func(t *testing.T) {
		s := newSvc(t)
		wantErr(t, s.CreateStream(0, "a"), ingest.ErrStreamExists, "重复建流")
		wantErr(t, s.CreateStream(0, ""), ingest.ErrInvalidParam, "空key建流")
		if _, err := ingest.NewService(0, 1, 1); !errors.Is(err, ingest.ErrInvalidParam) {
			t.Fatalf("D=0 应报参数非法，得到 %v", err)
		}
		if _, err := ingest.NewService(60001, 1, 1); !errors.Is(err, ingest.ErrInvalidParam) {
			t.Fatalf("D 超界应报参数非法，得到 %v", err)
		}
		if _, err := ingest.NewService(1, 0, 1); !errors.Is(err, ingest.ErrInvalidParam) {
			t.Fatalf("W=0 应报参数非法，得到 %v", err)
		}
		if _, err := ingest.NewService(1, 1, 1_000_000_001); !errors.Is(err, ingest.ErrInvalidParam) {
			t.Fatalf("T 超界应报参数非法，得到 %v", err)
		}
	})
}

// TestDisconnectReconnect 断开后重连，新纪元首片带标记；断开时残片封成短片。
func TestDisconnectReconnect(t *testing.T) {
	s := mustService(t, 4000, 10000, 5000)
	if err := s.CreateStream(0, "k"); err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	mustConnect(t, s, 0, "k", false, 1)
	mustPush(t, s, 1000, "k", 1, 1500) // 残片 1500
	if err := s.Disconnect(2000, "k", 1); err != nil {
		t.Fatalf("Disconnect 失败：%v", err)
	}
	// 断开封出 seq0 [0,1500)，第 0 片不带标记。
	checkPlaylist(t, s, "k", 0, 1500, []int64{0}, 0, 0)
	// 无活动者时 Push/Disconnect 报未连接。
	wantErr(t, s.Push(3000, "k", 1, 1000), ingest.ErrNotConnected, "断开后 Push")
	wantErr(t, s.Disconnect(3000, "k", 1), ingest.ErrNotConnected, "重复断开")
	// 重连无需等待静默，新纪元首片带标记。
	mustConnect(t, s, 3000, "k", false, 2)
	mustPush(t, s, 4000, "k", 2, 4000) // seq1 [1500,5500) 带标记
	got, err := s.Playlist("k", 0, 5500)
	if err != nil {
		t.Fatalf("Playlist 失败：%v", err)
	}
	if got.Segments[0].Disc || !got.Segments[1].Disc {
		t.Fatalf("断开重连标记不符：seq0=%v seq1=%v", got.Segments[0].Disc, got.Segments[1].Disc)
	}
	if got.DiscontinuitySequence != 0 {
		t.Fatalf("首片为 seq0 时 DS 应为 0，得到 %d", got.DiscontinuitySequence)
	}
	checkPlaylist(t, s, "k", 1500, 5500, []int64{1}, 1, 0) // 首片自身标记不计入
}

// TestForceTakeover force 接管无需等待静默；空残片接管不产片。
func TestForceTakeover(t *testing.T) {
	s := mustService(t, 4000, 10000, 1_000_000_000)
	if err := s.CreateStream(0, "k"); err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	mustConnect(t, s, 0, "k", false, 1)
	// 无残片时 force 接管：不产生短片。
	mustConnect(t, s, 1, "k", true, 2)
	mustPush(t, s, 2, "k", 2, 1000) // 残片 1000
	// 有残片时 force 接管：残片封成 seq0（第 0 片不带标记）。
	mustConnect(t, s, 3, "k", true, 3)
	checkPlaylist(t, s, "k", 0, 1000, []int64{0}, 0, 0)
	// 纪元 3 首片带标记。
	mustPush(t, s, 4, "k", 3, 4000)
	got, _ := s.Playlist("k", 1000, 5000)
	if !got.Segments[0].Disc || got.Segments[0].Seq != 1 {
		t.Fatalf("纪元3首片应带标记：%+v", got.Segments[0])
	}
}

// TestSingleGOPOverD 单个画面组超过 D 独自成片。
func TestSingleGOPOverD(t *testing.T) {
	s := mustService(t, 4000, 10000, 5000)
	if err := s.CreateStream(0, "k"); err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	mustConnect(t, s, 0, "k", false, 1)
	mustPush(t, s, 1, "k", 1, 60000) // 超 D 独自成 seq0 [0,60000)
	got, err := s.Playlist("k", 0, 60000)
	if err != nil || len(got.Segments) != 1 || got.Segments[0].Dur != 60000 {
		t.Fatalf("单 GOP 超 D 应独自成片：%+v err=%v", got, err)
	}
}

// TestEvictionExactW 淘汰后窗口总时长恰等 W 允许。
func TestEvictionExactW(t *testing.T) {
	s := mustService(t, 5000, 10000, 5000)
	if err := s.CreateStream(0, "k"); err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	mustConnect(t, s, 0, "k", false, 1)
	mustPush(t, s, 1, "k", 1, 5000) // seq0 [0,5000)
	mustPush(t, s, 2, "k", 1, 5000) // seq1 [5000,10000)
	mustPush(t, s, 3, "k", 1, 5000) // seq2 [10000,15000)，淘汰 seq0 后恰等 W
	// lo=5000：from=4999 报已滑出，from=5000 正常。
	_, err := s.Playlist("k", 4999, 6000)
	wantErr(t, err, dvr.ErrSlidOut, "恰等 W 后 from<lo")
	checkPlaylist(t, s, "k", 5000, 15000, []int64{1, 2}, 1, 0)
}

// TestRejectedOpsKeepState 被拒绝的操作不改任何状态（含时钟与最近活跃时刻）。
func TestRejectedOpsKeepState(t *testing.T) {
	s := mustService(t, 4000, 10000, 5000)
	if err := s.CreateStream(0, "k"); err != nil {
		t.Fatalf("建流失败：%v", err)
	}
	mustConnect(t, s, 0, "k", false, 1)
	mustPush(t, s, 1000, "k", 1, 1000) // lastActive=1000，maxNow=1000
	// 大 now 但被拒（流不存在）：时钟不得前进。
	wantErr(t, s.Push(9000, "ghost", 1, 1000), ingest.ErrStreamNotFound, "不存在流 Push")
	mustPush(t, s, 2000, "k", 1, 1000) // 若时钟被推到 9000，此处会报回退
	// 大 now 但被拒（参数非法）：时钟不得前进。
	wantErr(t, s.Push(8000, "k", 1, 0), ingest.ErrInvalidParam, "非法 dur")
	// 旧纪元 Push 被拒：最近活跃时刻不得更新。
	wantErr(t, s.Push(7000, "k", 0, 1000), ingest.ErrTakenOver, "旧纪元 Push")
	// lastActive 仍为 2000：6000-2000=4000<5000 报占用；若被改为 7000 则不会占用。
	_, err := s.Connect(6000, "k", false)
	wantErr(t, err, ingest.ErrStreamBusy, "拒绝后最近活跃时刻应保持")
	// 流占用被拒同样不改状态。
	mustPush(t, s, 3000, "k", 1, 1000) // maxNow 仍为 3000 量级，未受影响
}
