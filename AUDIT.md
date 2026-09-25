# AUDIT - 语义逐条核对

## 第二节九条语义

1. **行尾**（`\r\n`/`\r`→`\n`，`\r\r\n`→`\n\n`）：`norm/norm.go` `step` 中待定 CR 的解析（`eol.Pend.Feed`，孤 `\r` 作为保留字节发 `\n`，`\r\n` 的 `\r` 留间隙删除）。测试：`norm_test.go TestSemantics`（含 `"\r\r\n"→"\n\n"`）、demo 第 1 行。
2. **行尾空白**（行尾空白删除、行中保留、纯空白行变空行）：`step` 中 EOL/EOF 时 `wsb.Reset()` 丢弃、普通字节前 `flushWS` 保留。测试：`TestSemantics`（`"a  \n"`、`"  \n"`、`"a \t b\n"`）、demo 第 2 行。
3. **末尾换行策略**：`norm.Finalize`（Keep/EnsureOne/Strip 三分支，截尾用 `span.Builder.TruncateOut`，补 `\n` 用 `Synth`）。测试：`TestPolicyTable`（4 样例 × 3 策略，同 DESIGN.md 表）、demo 第 3 行。
4. **跨切分点一致**：`Norm` 的待定态（`eol.Pend`、`ws.Buf`）只随字节流推进，与 `Write` 分块无关。测试：`TestSplitsAndTruncation`（每个切分点两刀 + 全前缀截断，比对参考实现 `refNorm`）、demo 第 4 行。
5. **幂等**：输出只经 `Finalize` 定型，二次规范化无删改。测试：`TestIdempotent`（7 输入 × 3 策略 `N(N(x))==N(x)`）、demo 第 5 行。
6. **偏移映射**：`span/span.go` `ToOrig`/`ToOut`（只存保留区间，被删字节前向映射到下一区间 OutLo；合成字节例外见 DESIGN.md §2，`Map.Synth` 暴露）。测试：`span_test.go TestToOut/TestToOrig/TestMonotonic`、`norm_test.go TestMapping`（互逆跳过合成位、单调、被删字节样例表）、demo 第 6、7 行。
7. **非法字节**：NUL 与非法 UTF-8 非严格模式原样通过（`step` 的 default 分支只按 ASCII 特判）；严格模式 `c==0 && Strict` 返回 `*norm.Error{KindNUL, Off}`，已输出保留、实例进终态（`n.err` 锁存）。测试：`TestSemantics`（`\x00`、`\xff\xfe` 透传）、`TestErrors`（KindNUL、终态后再写）、demo 第 8 行。
8. **上限与延迟缓冲**：`ws.Buf.Add` 超限返回 false→`KindWS` 拒绝（选择论证见 DESIGN.md §3）；`emit` 与 `Close` 检查 `MaxOut`→`KindOut`。测试：`TestErrors`（KindWS off=3、KindOut off=2、KindClosed）、demo 第 9 行。
9. **并行一致**：`par/par.go` 段内 `norm`（Base=全局偏移、Policy=Keep、不 Close）+ `merge` 顺序修正进位（待定 `\r`/空白串），策略全局应用一次。测试：`par_test.go TestConsistency`（K=1..8 + 全部单切点 × 3 策略，输出与映射全偏移比对）、demo 第 11 行。

## 第四节复杂度实测

- 10 万行混杂输入（`par_test.go TestNormScale`、demo 第 12 行）：映射区间数 100001，单次 `ToOrig` 检查区间数 17 ≤ 2·log2(100001)+4 ≈ 37（二分，`span.Map.checked` 计数）。
- 10 MB 纯 `\n` 文本（无任何删除点）：映射区间数 1 ≤ 常数，不随输出字节数增长（`Builder.Keep` 相邻合并）。
- `span_test.go TestCheckedBound`：1/7/1000/100000 区间四档，检查数均 ≤ 2·log2(n)+4。

## 第五节故障注入

- 截断遍历：`TestSplitsAndTruncation` 对每个截断点 `Close()` 后与 `refNorm(前缀)` 比对；demo 第 10 行。
- 四类错误可区分（`*norm.Error.Kind`：NUL/WS/Out/Closed，均带原文偏移）：`TestErrors`、`par_test.go TestErrors`（并行下全局偏移）。

## 第六节并发

- `par_test.go TestDeterministic`：同一输入 K=8 重复 50 次输出与映射逐位相同；8 个 `norm` 实例并发互不串扰；`go test -race ./...` 干净；无 sleep。
- 单个 `norm` 实例非并发安全：见 `norm/norm.go` 包注释。
