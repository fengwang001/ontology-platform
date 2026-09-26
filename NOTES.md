# 定界帧编解码 NOTES

## 三、推导：三帧 `"hi"` / `"a\nb"` / `"x\y"`

| 负载 | 特殊字节 | 转义后帧字节（hex，含结尾 0A） |
|---|---|---|
| `hi` = 68 69 | 无 | 68 69 0A |
| `a\nb` = 61 0A 62 | 0A → 5C 6E | 61 5C 6E 62 0A |
| `x\y` = 78 5C 79 | 5C → 5C 5C | 78 5C 5C 79 0A |

整条流：`68 69 0A 61 5C 6E 62 0A 78 5C 5C 79 0A`

- (甲) 第 2 帧正确解码：3 字节 `61 0A 62`（`a\nb`）。只按裸 `\n` 切分、不反转义：4 字节 `61 5C 6E 62`（字面 `a` `\` `n` `b`）。
- (乙) 帧含 `5C 78`：`x` 非 `\` 非 `n`，非法转义；正确实现整体失败 `(nil, esc.ErrInvalidEscape)`；naive 得 2 字节 `5C 78`。
- (丙) 帧 `61 62 5C` 后随分隔符：`\` 是悬空转义；正确实现整体失败 `(nil, esc.ErrDanglingEscape)`；naive 得 3 字节 `61 62 5C`（或丢弃 `\` 得 2 字节 `61 62`）。

## 二、四条不变量落点

1. 往返一致：`frame.Decode` 单趟反转义是 `frame.Encode` 的严格逆（frame/frame.go）；钉住：`TestRoundTrip`。
2. 转义可逆：`esc.Escape`/`esc.Unescape` 逐字节 `5C→5C 5C`、`0A→5C 6E`（esc/esc.go）；钉住：`TestEscapeRoundTrip`。
3. 与朴素参照一致：`api.SelfCheck` 内置 naiveEncode 逐字节比对（api/api.go）；钉住：`TestEncodeMatchesNaive`。
4. 失败不留痕：`Decode` 遇错 `return nil, err`，`NextFrame` 出错不写 `r.pos`（frame/frame.go）；钉住：`TestDecodeErrors`、`TestReaderCursorOnError`。
