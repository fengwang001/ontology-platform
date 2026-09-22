package codec

import "testing"

// 回归：钉住契约「Need 是还差多少字节才完整」。
// 半条记录的缺口必须是「整条记录编码长度减去已到场字节数」，
// 而不是由固定开销算出的常数或负数；下游按 Need 决定补传字节数。
func TestRegressTruncatedNeedIsRemainingBytes(t *testing.T) {
	payload := []byte("hello world") // 11 字节负载，整条编码 19 字节
	enc := Encode(payload)

	// 长度前缀完整、负载只来了一部分：缺 19-7=12 字节。
	mid := Decode(enc[:PrefixLen+3])
	if mid.Kind != KindTruncated || mid.Reason != TruncPayload {
		t.Fatalf("kind=%v reason=%v, want Truncated/Payload", mid.Kind, mid.Reason)
	}
	if want := len(enc) - (PrefixLen + 3); mid.Need != want {
		t.Fatalf("need = %d, want %d (encodedLen - arrived)", mid.Need, want)
	}

	// 已到场字节数超过固定开销：缺口仍为正，不得算成负数。
	later := Decode(enc[:len(enc)-2])
	if later.Kind != KindTruncated || later.Reason != TruncChecksum {
		t.Fatalf("kind=%v reason=%v, want Truncated/Checksum", later.Kind, later.Reason)
	}
	if later.Need != 2 {
		t.Fatalf("need = %d, want 2", later.Need)
	}

	// 补传 Need 字节后必须恰好解出完整记录。
	full := Decode(enc[:PrefixLen+3+mid.Need])
	if full.Kind != KindOK || string(full.Payload) != string(payload) {
		t.Fatalf("after supplying Need bytes: kind=%v payload=%q, want OK/%q",
			full.Kind, full.Payload, payload)
	}
}
