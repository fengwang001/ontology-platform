# 折叠口径推导

| 输入 | 逐 rune 简单折叠 | 完整折叠 |
|---|---|---|
| `STRASSE` | `strasse` | `strasse` |
| `strasse` | `strasse` | `strasse` |
| `straße` | `straße` | `straße` 的 `ß` 展开为 `ss`，得 `strasse` |
| `İ` (U+0130) | `i` | `i` 加 U+0307（组合点） |

完整折叠不是长度保持映射：朴素地把展开结果再交给逐 rune 轮转会使 `ß -> ss`，再从 `s` 轮转到 `S`，从而破坏幂等；要修复必须做成闭包。长度变化还使索引键不能兼任展示键，否则 `ß` 被取回成 `ss`，原始写法丢失，必须另存原始键。

选择逐 rune 规范代表元：单遍、长度保持，且代表元函数天然幂等。此口径下 `straße != strasse`，二者不命中同一记录；这不违反命中一致，因为该不变量只要求折叠相等才命中、折叠不等互不命中。

# 不变量保证

1. 折叠幂等：`fold.canonical` 在等价环中固定选择小写代表元，`Folder.Fold` 逐 rune 映射；由 `TestFoldIdempotence` 钉住。
2. 命中一致：`keymap.Map` 只以 `foldKey` 查写 map；由 `TestLookupConsistency` 钉住。
3. 原始键保真：`keymap.entry.original` 仅首次插入写入，重复插入只改 `value`；由 `TestOriginalKeyPreservation` 钉住。
4. 顺序确定：`api.Table.Keys` 排序折叠键后映射回原始键；由 `TestKeysOrderIndependent` 钉住。
5. 失败不留痕：`keymap.Map.Put` 在加锁写表前完成空键、长度、容量判定；由 `TestRejectedOperationsLeaveNoTrace` 钉住。
自检：`api.Table.SelfCheck` 校验折叠键、唯一性和数量；由 `TestSelfCheck` 钉住。
