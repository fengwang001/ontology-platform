package codec

import "testing"

// 回归测试：钉住契约「Need 是还差多少字节才完整，
// 即整条记录编码长度 − 已到场字节数」。
// 修复前 Need 被算成 HeaderLen − len(buf)，在负载较长时
// 会报出固定小数字甚至负数，下游按它补传永远补不齐。
func TestDecodeTruncatedNeedIsTrueGap(t *testing.T) {
	payload := []byte("0123456789") // 10 字节负载，整条编码 18 字节
	enc := Encode(payload)

	// 长度前缀完整、负载只来了一部分：TruncPayload。
	present := PrefixLen + 3 // 到场 7 字节，缺 18-7=11
	res := Decode(enc[:present])
	if res.Kind != KindTruncated || res.Reason != TruncPayload {
		t.Fatalf("kind=%v reason=%v, want Truncated/Payload", res.Kind, res.Reason)
	}
	want := len(enc) - present
	if res.Need != want {
		t.Fatalf("need = %d, want %d (encoded %d - present %d)",
			res.Need, want, len(enc), present)
	}
	if res.Need <= 0 {
		t.Fatalf("need must be positive for a truncated record, got %d", res.Need)
	}

	// 负载完整、校验和只来了一部分：TruncChecksum，同样按真实缺口报。
	present = PrefixLen + len(payload) + 1 // 缺 3 字节校验和
	res = Decode(enc[:present])
	if res.Kind != KindTruncated || res.Reason != TruncChecksum {
		t.Fatalf("kind=%v reason=%v, want Truncated/Checksum", res.Kind, res.Reason)
	}
	if want := len(enc) - present; res.Need != want {
		t.Fatalf("need = %d, want %d", res.Need, want)
	}
}
