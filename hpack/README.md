# hpack：带动态表尺寸协商的 HPACK 式头部压缩

包路径：`ontology/hpack`。入口类型为 `Encoder` / `Decoder`，均支持并发调用
（内部互斥，等价于某个串行顺序）。

```go
enc, _ := hpack.NewEncoder(60, 65536, map[string]bool{"token": true}) // C0, L0, 敏感名
dec, _ := hpack.NewDecoder(60, 65536)                                  // C0, Ld

block, err := enc.Encode([]hpack.Header{{Name: "k", Value: "v"}})
headers, err := dec.Decode(block)

_ = enc.Resize(32)    // 在对端上限内调整本端容量
_ = enc.SetLimit(100) // 更新对端通告上限（小于当前容量时立即缩表）
```

`Block` 由 `Updates []int`（容量协商）与 `Instrs []Instruction` 组成，指令为
`Indexed`、`LiteralIndexed`、`LiteralNever` 三种。

## 表与索引

- 静态表固定 4 项：`1=(:method,GET)`、`2=(:method,POST)`、`3=(:path,/)`、
  `4=(:scheme,https)`。
- 动态表为有序条目，最新插入者位于最前；动态索引从 5 起，越旧索引越大。
- 条目大小公式（按字节数）：`size = len(name) + len(value) + 32`；
  `used` 为当前所有动态条目大小之和。
- 构造参数约束：`1 ≤ C0 ≤ L0 ≤ 65536`（解码器为 `1 ≤ C0 ≤ Ld ≤ 65536`），
  越界返回 `ErrInvalidArgument`。

## 插入、驱逐与清空

`Insert(name, value)`：

1. 若 `size > 当前容量 C`：**清空整张表且不加入该条目**（不是错误）。
2. 否则从最旧条目起逐条驱逐，直到 `used + size ≤ C`。
3. 将该条目作为最新项（索引 5）加入。

名字合法：1–256 字节，只含小写字母、数字与 `- : _ .`；
值合法：0–1024 字节（任意字节）。

## Encode 的逐项判定

按头部顺序逐项处理，前一项的插入对后一项立即可见。**所有索引都在本项
`Insert` 之前解析**，解码器同样如此。

1. 名字精确命中敏感名集合：输出 `LiteralNever(nameIdx, name, value)`，
   **不插入**；`nameIdx` 仍按下面的仅名字匹配规则解析。
2. 否则先找 `(name,value)` 全匹配：静态表优先，其次动态表取最新者；
   命中输出 `Indexed(i)`。
3. 无全匹配：输出 `LiteralIndexed(nameIdx, name, value)` 并 `Insert`。
4. `nameIdx` 取仅名字匹配：静态表取最小索引，其次动态表取最新者，都没有则
   为 `0`；`nameIdx != 0` 时携带的字面名字为空串，`nameIdx == 0` 时携带完整
   字面名字。

编码方遇到任一非法头部，整个调用返回 `ErrInvalidArgument`：不插入任何条目，
  `base` 与 `mn` 也不变。

## 尺寸协商（updates 生成规则）

- `SetLimit(L')`：要求 `1 ≤ L' ≤ 65536`；若 `L' < C`，立即令 `C = L'` 并按
  驱逐规则缩表。
- `Resize(C')`：要求 `1 ≤ C' ≤ 当前上限`，令 `C = C'` 并缩表。
- `base`：上一次 `Encode` 结束时的 `C`（构造后为 `C0`）。
- `mn`：自 `base` 起 `C` 出现过的最小值。
- 每次 `Encode` 开头生成 `updates`：
  - 若 `mn < base` 且 `mn < 当前 C`：`[mn, 当前 C]`（先降后升，两项）；
  - 否则若 `当前 C ≠ base`：`[当前 C]`；
  - 否则为空。
- `Encode` 成功后令 `base = 当前 C` 并重置 `mn = 当前 C`。

例：`C=60` 时 `Resize(10)` 再 `Resize(60)` → `updates=[10,60]`；
只 `Resize(10)` → `[10]`；只升高到 80（上限允许）→ `[80]`。

## Decode、错误类别与回滚

解码顺序：先校验**全部** updates（每项须 `1 ≤ v ≤ Ld`，否则 `ErrUpdate`，
此时尚未应用任何一项），通过后按序应用容量并缩表，再按序处理指令。

- `Indexed(i)`：`1..4` 取静态表，`5+` 取当前动态表（含本块此前指令的插入）；
  `i == 0` 或越界为 `ErrIndex`。
- 字面指令：`nameIdx > 0` 时名字取该索引条目（在本项插入之前解析），且不得
  再携带字面名字；`nameIdx == 0` 时必须携带合法字面名字，否则 `ErrSyntax`。
  值不合法也是 `ErrSyntax`。**同一指令先判 `ErrSyntax` 再判 `ErrIndex`**。
- `LiteralIndexed` 解析成功后执行同样的 `Insert`；`LiteralNever` 不插入。

任一错误都使整个块原子回滚：动态表、容量与返回头部全部恢复到调用前，
且不输出任何头部。编码方所有被拒绝的操作（非法头部、`Resize`/`SetLimit`/
构造参数越界）同样不改变任何状态。

## 本地验证

```bash
go test -race -v ./hpack/
go test -cover ./hpack/
```

关键用例（见 `hpack/hpack_test.go`，`-v` 日志打印输入、输出与判定依据）：

- `C=60` 空表编码 `(k,v)` → `LiteralIndexed(0,"k","v")`，表内 1 条，大小 34。
- 再编码 `(k,w)` → 仅名字匹配动态索引 5，得
  `LiteralIndexed(5,"","w")`；`34+34=68>60` 驱逐 `(k,v)`；解码器同样在驱逐前
  按索引 5 取名字。
- `C=40` 编码 `(abcdefgh,ijklmnopq)`（`8+9+32=49>40`）→ 输出字面指令但表被
  清空且不加入。
- 静态全匹配优先于动态全匹配；仅名字匹配时静态最小索引优先（`:method` 取 1）。
- 敏感名不插入但名字索引照取。
- 先降后升回原值 → updates 为 `[mn, cur]` 两项。
- 第三条指令出错时前两条的插入被回滚。
- 随机头部流 + 随机 `Resize`/`SetLimit` 下两端表逐项一致
  （`TestRandomStreamTableParity`，多 seed）。
- 确定性：相同输入序列产生完全相同的块与表（`TestDeterministicBlocks`）。
- 并发：8 协程 × 25 轮 `Encode/Decode`，`-race` 下通过
  （`TestConcurrentAccess`）。
