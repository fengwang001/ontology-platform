# 前向安全的密钥演进审计日志（auditlog）

包路径：`ontology/auditlog`

## 1. 三个注入函数与配置

所有密码学材料均为 `uint64`，由构造时注入的三个确定性函数产生：

- `evolve(k uint64) uint64`：普通写入（`Append`/`Seal`）后推进密钥。
- `rekey(k uint64, i int64) uint64`：换钥记录处替代 `evolve` 推进密钥。
- `mac(k uint64, i int64, typ int32, ts int64, data []byte) uint64`：条目标记。

`New(k0, cap, maxData, evolve, rekey, mac)` 的合法性要求（否则返回
`ErrInvalidConfig`）：`2 <= cap <= 10^6`、`0 <= maxData <= 4096`、三个函数
均非 nil。时间戳合法区间为 `[0, 10^15]`。

## 2. 写入者状态与操作

写入者只保存：当前密钥 `ki`（初值 `k0`）、下一条序号 `i`（初值 0）、最近
时间戳 `lastTs`（初值 0）、已封存标志与条目切片。封存记录与换钥记录的
`Data` 均为自身序号 `i` 的十进制 ASCII 文本。

- `Append(ts, data)`：写 `Typ=0`，`Tag = mac(ki,i,0,ts,data)`，随后
  `ki = evolve(ki)`、`i++`、`lastTs = ts`。旧密钥立即丢弃。
  拒绝次序（只报第一个）：参数非法（`ts` 越界或 `len(data) > maxData`）、
  已封存（`ErrSealed`）、时间回退（`ts < lastTs`，`ErrTimeRollback`）、
  容量已满（写入后总条数需 `<= cap-1`，`ErrCapFull`）。
- `Seal(ts)`：写 `Typ=1`，密钥同样 `evolve` 推进，并置已封存；此后
  `Append`/`Rekey`/`Seal`/`Export` 全部以 `ErrSealed` 拒绝。拒绝次序：
  参数非法、已封存、时间回退。Seal 可使用 `cap` 的最后一个位置。
- `Rekey(ts)`：写 `Typ=2`，密钥变为 `rekey(ki, i)`（不调用 evolve），
  占一条容量、不置封存。拒绝次序与容量规则同 Append（参数只校验 `ts`）。
- `Export()`：封存前返回一致的 `(i, ki)`，封存后返回 `ErrSealed`。
- `Entries()`：返回条目的深拷贝快照。

任何被拒绝的操作都不改变条目、密钥、序号与 `lastTs`。全部方法以
`sync.RWMutex` 保护，等价于某个串行顺序；`Export` 在同一临界区内读取
`(i, ki)` 保证一致，`SelfVerify`/`Entries` 在锁内复制快照。

## 3. 一趟检查点校验

`Verify(entries, checkpoints)` 的检查点为 `(序号, 密钥)` 列表：集合非空、
序号互不相同且 `>= 0`，否则返回 `ErrInvalidArg`。设最小序号 `j0`。

- 位置 `t < j0` 的条目完全不看，只计入不可校验数
  `u = min(j0, m)`（`m = len(entries)`）。
- 从 `t = j0` 开始，令 `k` 等于 `j0` 处检查点密钥，单趟递增：
  1. 若 `t != j0` 且存在序号为 `t` 的另一个检查点，比较其密钥与当前 `k`：
     不等报 `checkpoint_conflict`，位置 `t`，立即停止。
  2. 按固定次序检查条目 `entries[t]`：
     - `Index != t`：`Index > t` 报 `gap`；`Index < t` 报 `replay`；
     - `t > j0` 且位置 `t-1` 是封存记录：`append_after_seal`（因此缺口先于
       封存后追加，Index 不等先返回）；
     - `t > j0` 且 `Ts < entries[t-1].Ts`：`time_rollback`（因此时间回退
       先于标记比较；`t == j0` 处不做时间回退检查）；
     - `Typ` 不属于 `{0,1,2}`：`tampered`（不调用 mac）；
     - `Tag != mac(k, t, Typ, Ts, Data)`：`tampered`；
     - `Typ` 为 1 或 2 且 `Data != decimal(t)`：`content_mismatch`。
  3. 全部通过后该条计入已校验；`Typ=2` 时 `k = rekey(k, t)` 且
     `RekeyCalls++`，否则 `k = evolve(k)` 且 `EvolveCalls++`。
- 处理完 `m` 条后：若 `m` 处存在检查点（且 `m != j0`），与最终 `k` 比较，
  不等报 `checkpoint_conflict`（位置 `m`）；其后若仍有序号 `> m` 的检查点，
  报 `truncated`，位置 `m`。
- 无错误时 `Sealed` 为“最后一条（位置 `m-1`）是通过校验的封存记录”。

报告字段：`Kind`、`Pos`、`Verified`、`Unverifiable`、`EvolveCalls`、
`RekeyCalls`、`Sealed`。计数器只在某条完整通过、真正应用一次密钥转移时
递增，因此 `EvolveCalls` 恰等于已校验的非换钥条数、`RekeyCalls` 恰等于
已校验的换钥条数；第一个错误处的密钥转移不发生、也不计数。中间检查点
直接与单趟推导出的 `k` 比较，绝不从头重算。`SelfVerify` 对自身快照执行
同一算法。

## 4. 前向安全的适用范围

安全性依赖注入函数的性质：`evolve`/`rekey` 必须单向，`mac` 必须在不知道
当前密钥时不可伪造。规范给出的线性示例函数
（`evolve(k)=(3k+11) mod 1000003` 等）便于手算核对，但线性演进可逆推，
不具备真实前向安全性；测试中的前向安全用例
（`forwardsecure_test.go`）改用 SHA-256 折成 uint64 的 `evolve`/`rekey`/
`mac`。

性质：写入者每写一条即丢弃旧密钥，密钥泄露后历史条目的标记无法被重算；
持有第 `c` 条检查点密钥的校验者可验证 `c` 之后（直到下一次密钥泄露）的
整段日志，攻击者拿到 `k_c` 可以任意重写位置 `c` 及其之后的条目并通过
校验，但改动 `c` 之前的任何条目都会与更早的检查点冲突或标记不符。
检查点密钥本身是带外信任根：它不保护其之前的历史（那段为不可校验范围），
也不防止持有同一密钥的攻击者伪造之后的记录。时间戳只保证在已校验范围内
单调不减（相等允许），不防止写入者自己预写或与外部时钟的偏差。

## 5. 本地验证方法

```bash
go test ./auditlog -v
go test ./auditlog -run TestDifferentialAgainstNaive2000 -v
go test -race ./...
go vet ./... && gofmt -l .
```

对照测试中 `naiveVerify` 是独立的朴素实现：对每个检查点都从 `j0` 重新
逐跳演进再比较（允许 O(n^2)），与生产实现比较类别、位置、已校验条数、
不可校验数、两个调用计数与封存标志；`-v` 输出每组输入操作、检查点、
突变方式、报告类别与判定依据。
