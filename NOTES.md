# NOTES — 帧定界与字节转义

## 第三节推导

`Frame([0x41])` = `7E 41 7E`；`Frame([0x7E,0x7D])` = `7E 7D 5E 7D 5D 7E`。拼接得 9 字节 `7E 41 7E 7E 7D 5E 7D 5D 7E`。

| 步骤 | 喂入字节 | 本步产出帧 | 之后残留状态 |
|---|---|---|---|
| Feed#1 | `7E 41 7E 7E 7D` | `[0x41]`（7E 开帧、41 入 payload、7E 收帧；第 4 字节 7E 再开帧、7D 进 ESC） | `ESC`，payload 前缀 `[]` |
| Feed#2 | `5E 7D 5D 7E` | `[0x7E,0x7D]`（5E^20=7E 入 payload；7D 进 ESC；5D^20=7D 入 payload；7E 收帧） | `OUT`，前缀 `[]` |
| Flush | — | 无 | `OUT`，无错误 |

- (甲) 不转义时 `Frame([0x7E,0x7D])` = `7E 7E 7D 7E`。解码：第 2 个 `7E` 被误当**收帧定界符** → 产出一个空帧；`7D` 在 OUT 态被误当**噪声**丢弃；末尾 `7E` 又开一帧却无人收 → `Flush` 报 `ErrTruncatedFrame`。payload 的 `0x7E` 被误当帧界、`0x7D` 被误当噪声。
- (乙) 若两次 Feed 间丢状态（重置 OUT）：第二次 Feed 的 `5E 7D 5D` 全被当噪声忽略，`7E` 开新帧 → 产出零帧，**丢失整个 `[0x7E,0x7D]` 帧**（等待中的转义还原 `5E→7E` 被丢弃），Flush 还会误报截断帧。正确实现产出 `[0x7E,0x7D]`，Flush 正常。
- (丙) `7E 7E 7E 41 7E` 正确解出**两帧**：空 payload 帧 + `[0x41]`。「连续 FLAG 判错」会把合法的 `7E 7E` 空帧当错误、中断解析；「丢弃空帧」则只产出 `[0x41]`，帧数从 2 变 1，与发送端帧计数永久错位。

## 四条不变量的保证位置与钉住测试

1. 往返一致：`frame.Encode` 对 `esc.NeedsEscape` 字节一律 `ESC+Map`，`Parser.Feed` 的 ESC 态用 `esc.Map` 还原（XOR 对合）→ `TestRoundTrip`。
2. 切分点无关：`frame.Parser` 的 `st/buf/frames/checked` 是跨 `Feed` 保持的字段，`Feed` 不重置 → `TestSplitInvariance`。
3. 定界无损：`Encode` 转义全部 FLAG/ESC；解码 ESC 态遇 FLAG 报 `ErrBadEscape`；`api.SelfCheck` 逐帧核验帧体无裸 FLAG/ESC → `TestRoundTrip`、`TestSelfCheck`。
4. 失败不留痕：`Parser.Feed` 全程在局部变量上解析、成功才提交；`Flush` 报错不改状态 → `TestFaults`（含被拒后可继续正常使用）。
