# 带转义的 RLE 编解码器：设计推导

## 严格解码的拒绝清单（由不变量 Encode(Decode(t))==t 推导）

不变量：被严格解码器接受的每个 t，都是 Decode(t) 的**唯一规范编码**。
因此任何「解码结果的规范编码 ≠ 自身」的 t 都必须拒绝。逐条推导：

- **显式次数 1**：若接受 `1a`，Decode=`a`，Encode(`a`)=`a` ≠ `1a`。拒绝。
- **次数 0**：`0a` 无论解释为 0 个还是 1 个 `a`，Encode 结果分别是 `` 或 `a`，都不是 `0a`。拒绝。
- **前导零**：若接受 `03a`，Decode=`aaa`，Encode=`3a` ≠ `03a`。拒绝（`0` 本身按上一条拒绝，`0x…` 归入本条）。
- **相邻同符号游程**：若接受 `2a3a`，Decode=`aaaaa`，Encode=`5a` ≠ `2a3a`（Encode 必取最长游程）。拒绝。
- **`\` 后非数字非反斜杠**：若 `\a` 表示 `a`，则 Encode(`a`)=`a` ≠ `\a`；任何其它解释同样不唯一。拒绝。
- **末尾孤立 `\`**：任何 s 的 Encode 结果中 `\` 必与后随的数字/反斜杠成对出现，不存在以孤立 `\` 结尾的规范编码。拒绝。
- **末尾只有次数**：任何 s 的 Encode 结果都以符号结尾，不存在以数字结尾的规范编码。拒绝。
- **非法 UTF-8**：Decode 输出必须是合法 UTF-8 字符串，且 Encode 按码点切分，非法字节序列没有规范来源。拒绝。

实现：每类一个哨兵错误（`errors.Is` 可判定），统一包在 `*rle.DecodeError{Kind, Off}` 中携带字节偏移。

## 大次数的取舍

**支持**任意大次数：`runs.Count` 用 `math/big.Int` 解析，无整数溢出。
解码侧不按次数预分配：发射前用「剩余额度 ÷ 符号字节数」与次数比较
（次数放不进 uint64 时直接判超限），超限返回哨兵 `ErrOutputLimit`。
上限可配置：`Decode` 内置 1<<30 字节上限，`NewDecoder(limit)` 自定，
负值表示不限（调用方自负其责）。例：`99999999999999999999a` 在默认上限下
返回 `ErrOutputLimit`，不溢出、不崩溃。

## 语义-测试对照表

| 第三节语义 | 钉住它的测试 |
|---|---|
| 1 往返 Decode(Encode(s))==s | `rle.TestRoundtrip`（固定+随机 300 例）、`rle.TestEncodeDecode` |
| 2 严格解码拒绝清单+偏移 | `rle.TestDecodeReject`（8 类错误各带偏移断言） |
| 3 大次数不溢出、上限可配置 | `rle.TestBigCount`（2^64 与 1e20 次数、limit 0/5/9/20） |
| 4 最长游程、不做 Unicode 规范化 | `rle.TestEncodeDecode`（`éé`→`2é`、`e+U+0301` 不合并）、`runs.TestSplit` |
| 5 任意切分点结果/错误逐位相同 | `rle.TestSplitPoints`（每个切分点一刀 + 逐字节喂入，含 `2a3a`） |
| 计数器 == 输入字节数 | `rle.TestCheckedBytes`（1 MiB 逐字节喂入） |
