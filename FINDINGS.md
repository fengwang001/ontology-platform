# FINDINGS

## 测试批次记录

- `point`：16B 小端编解码（含负时间戳、±Inf、亚正规数）逐位往返一致；
  NaN 被 `Valid()` 拒绝（`ErrNaN`）；15B 残片批量解码返回 `ErrShortBuffer`。
- `agg`：First/Last/Min/Max/Mean/Count 正确（含乱序、全负、±Inf）；
  +Inf 与 -Inf 同桶 Mean 按 IEEE 754 为 NaN；空累加器 Count=0。
- `align`：`ts=-1,step=10 → -10`；非法 step 报错；左闭右开三位置全部正确；
  同批点洗牌 20 次与排序输入逐位一致；100,001 桶/上限 100 时峰值 100、
  处理数 = 输入数；8 协程并发 `-race` 干净，结果与串行多重集一致。

<!-- TABLES -->
