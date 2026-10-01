# regbank：带访问类型的 32 位寄存器位域模拟器

`regbank` 以位域（field）为最小单位建模一组 32 位寄存器，精确复现总线读、
带字节使能的总线写、软件读改写（RMW）以及硬件侧置位的副作用与返回值。
所有访问互斥到单个寄存器，可并发调用，结果等价于某个串行顺序。

## 登记

```go
b, err := regbank.New([]regbank.RegDef{{
    Name: "CTRL",
    Fields: []regbank.FieldDef{
        {Name: "EN",    Lo: 0,  Width: 1, Access: regbank.RW,  Reset: 0},
        {Name: "MODE",  Lo: 4,  Width: 3, Access: regbank.RO,  Reset: 2},
        {Name: "FLAGS", Lo: 16, Width: 4, Access: regbank.W1C, Reset: 0xF},
    },
}})
```

每个位域给出名字、最低位 `Lo`、位宽 `Width`、访问类型 `Access` 与复位值
`Reset`。位域初始存值即复位值；未被任何位域覆盖的位读为 0、写被忽略。

以下布局问题全部返回同一个错误 `regbank.ErrLayout`，且整个 `New` 原子失败
（不会留下部分登记的寄存器）：

- 寄存器名为空，或寄存器名重复；
- 位域名为空，或同一寄存器内位域名重复；
- `Lo < 0`、`Width < 1`、`Lo + Width > 32`；
- 两个位域在位上重叠；
- `Reset >= 2^Width`。

## 访问类型语义

| 类型 | 写 (`Write`)                         | 读 (`Read`)               |
|------|--------------------------------------|---------------------------|
| RW   | 写入即存值                           | 返回当前存值              |
| RO   | 写被忽略（仅 `HwSet`/`Reset` 可改） | 返回当前存值              |
| WO   | 写入即存值                           | 返回 0（`Raw` 仍可见存值）|
| W1C  | 写入位为 1 → 该位清 0；写 0 不变     | 返回当前存值              |
| W1S  | 写入位为 1 → 该位置 1；写 0 不变     | 返回当前存值              |
| RC   | 写被忽略                             | 返回当前存值，随后位域清 0|

所有位域存值始终满足 `0 <= value < 2^Width`。

## 字节使能（Byte Enable）

`Write(reg, val, be)` 的 `be` 取值 0..15，bit i 使能 32 位字的第 i 个字节
（bit0 为最低字节）。**位域只有在它覆盖到的每一个字节都被使能时才参与本次写**，
按其访问类型处理 `val` 中属于它的那些位；只要有一个覆盖字节未被使能，该位域
整体保持不变。例如 `Lo=4, Width=8` 的位域横跨字节 0 与字节 1，`be=1` 或
`be=2` 时都不变，只有 `be=3` 才更新。`be=0` 对所有位域都没有效果。

## 读改写（ReadModifyWrite）

`ReadModifyWrite(reg, mask, value)` 在同一寄存器锁内严格展开为：

1. 执行一次 `Read`，得到带读副作用的返回值 `r`（WO 位读作 0，RC 位被清零）；
2. 计算 `w = (r &^ mask) | (value & mask)`；
3. 以 `be=15` 执行一次 `Write(w)`；
4. 返回 `r` 与写后的**原始存值**（`Raw`，WO 位可见）。

因此存在两类需要精确复现的“软件陷阱”：

- **W1C 误清**：读出为 1 且不在 `mask` 内的位，会被原样写回的 1 清掉；
  反之，在 `mask` 内且 `value` 对应位为 0 时，写回 0，位不会被清。
- **WO 误覆盖**：不在 `mask` 内的 WO 位读作 0，写回的 0 会覆盖其真实存值。
- RC 位也一样：步骤 1 的读先把 RC 清零，步骤 3 对 RC 的写又被忽略，
  所以 RMW 后 RC 必为 0（除非之后 `HwSet`）。

## 其他 API

- `HwSet(reg, field, v)`：硬件侧直接把位域存值设为 `v`，不受访问类型约束
  （RO 也能改），但要求 `v < 2^Width`。
- `Raw(reg)`：无任何副作用地返回各位域当前存值拼成的字（WO 位可见、RC 不清除）。
- `Reset()`：把所有寄存器的全部位域恢复为复位值。

## 错误原因与优先级

运行期错误彼此可区分（`errors.Is` 判定），被拒绝的操作不改变任何存值，
被拒绝的 `Read` 也不触发 RC 清零。多个问题同时存在时只报第一个，顺序为：

1. `ErrNoRegister`：寄存器不存在（对所有操作最先检查）；
2. `ErrInvalidBE`（`be < 0` 或 `be > 15`）/ `ErrNoField`：仅适用于相关操作；
3. `ErrValueRange`：`HwSet` 的 `v >= 2^Width`。

## 本地验证

```bash
# 普通全量测试（日志含每次访问的输入、输出与判定依据，用 -v 查看）
go test -v ./regbank/

# 竞态检测 + 确定性重放
go test -race -v ./regbank/

# 全包构建与静态检查
go build ./... && go vet ./...
```

测试内容：

- `TestAccessTable`：六种访问类型在复位 / 读 / 全 1 写 / 再读下的读写表；
- `TestW1CW1SZeros`：写 0 不变、选择性写 1；
- `TestByteEnable`：跨字节位域部分使能整体不变、`be=0` 无效果；
- `TestRCReadClear`：RC 读返回旧值并清零，连续第二次读为 0，被拒绝的读不清零；
- `TestReadModifyWrite`：W1C 误清、mask 内写 0 不清、WO 误覆盖；
- `TestLayoutErrors` / `TestAccessErrors` / `TestResetAndHwSet`：布局与运行期错误；
- `TestNaiveCrossCheck`：300 组随机定义 × 各 80 步随机操作，与同文件内独立的
  逐位朴素模拟逐步对照返回值、错误与原始存值；
- `TestReplayDeterminism`：同一脚本重放两次，返回值序列与最终存值完全相同；
- `TestConcurrent`：16 goroutine × 400 次混合访问，配合 `-race` 验证串行等价、
  未覆盖位恒为 0、RC 无 HwSet 介入时连续两次读第二次为 0。
