# base32 推导与不变量说明

## 填充推导（第三节）

每组 5 字节 = 40 位 = 8 字符；剩余 r 字节产生 ceil(8r/5) 个有效字符，其余补 `=`：

| 剩余字节数 | 有效字符数 | 填充个数 |
|---|---|---|
| 1 | 2 | 6 |
| 2 | 4 | 4 |
| 3 | 5 | 3 |
| 4 | 7 | 1 |

合法填充个数集合：{0, 1, 3, 4, 6}（0 对应整组）。1..7 中 2 和 5 永远不可能：
填充 p 意味着有效字符 8-p，承载 (8-p)*5 位，必须能整除 8 还原成整字节；
p=2 → 30 位、p=5 → 15 位，均非 8 的倍数，对应不到任何真实输入，
故解码遇到必须报错（ErrPaddingCount），不得容忍。

## 四条不变量的保证位置与钉住测试

1. 往返恒等：b32.Codec.Encode/Decode 按 5 字节分组做无损位重组（b32/b32.go），
   由 TestRoundTripAndShape 与 TestSelfCheck（调 codec.SelfCheck）钉住。
2. 编码长度确定：b32.encodeGroup 每组恒输出 8 字符、按推导表补 `=`，
   由 TestRoundTripAndShape 的长度与填充断言钉住。
3. 非法输入可判定：b32.Codec.Decode 对非法字符/中间填充/非法填充个数分别返回
   哨兵错误 ErrInvalidChar/ErrPaddingPosition/ErrPaddingCount，由 TestDecodeErrors 钉住。
4. 失败不留痕：b32.Codec.Decode 出错时返回 nil 而非半截结果，
   Go 字符串不可改故入参不会被改，由 TestDecodeErrors 钉住。

分组计数器为 b32.Codec 的非导出字段，单遍每组只计一次，由 TestGroupCounts（1000/100000 字节）钉住；并发安全由 TestConcurrent 钉住。
