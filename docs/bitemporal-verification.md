# 本地验证方法与结果

以下命令均在仓库根目录执行。若系统未安装 Go 或 `go` 不在 PATH：

```bash
export PATH=$PATH:/usr/local/go/bin
export GOCACHE=/tmp/go-cache   # 当默认缓存目录只读时
```

## 全量测试

```bash
go test ./...
go test -race -v ./bitemporal/...
```

覆盖内容：

- `TestAxisPresenceMatrix`：两条轴记录与否四种组合及双向迁移；
  V2→V1 降级必须报告“损失有效时间轴”，V1→V2 升级必须报告固定填充规则；
- `TestBoundarySemantics`：V2`[)`→V3`[]` 在端点翻转归属（不兼容、附见证点）；
  无界区间的闭合位差异不改变归属（兼容）；V2→V3V 只报告有效轴；
- `TestRejectedRecord`：起点晚于终点先于边界不兼容、先于未知版本；
- `TestUnknownVersion`：源/目标未知分别与合并报告；
- `TestJudgeDoesNotMutateAndIsStable`：64 goroutine 并发结果逐字段一致、
  100 次重复判定不变、入参记录不变；
- `TestPathVsDirect` / `TestPathRepeatedIdempotence`：连续迁移路径与直达
  对照（单调链等价、先降级再升级的绕行损失被显式报告、失败跳定位、50 次稳定）；
- `TestRandomVersusNaiveReference`：4000 组随机场景与朴素参照模型对照，
  判定输入/输出/依据写入 JSONL 日志，路径见测试输出；
- `TestLinearScaling` 与 `BenchmarkJudgeBatch_*`：线性扩展验证。

## 复杂度基准

```bash
go test -run=^$ -bench=BenchmarkJudgeBatch -benchtime=200x ./bitemporal/judge
```

一次本机实测（linux/arm64，Go 1.26.5）：

| 记录数 | ns/op | 相对 100 条的倍率 |
| --- | --- | --- |
| 100 | 50,511 | 1.0x |
| 1,000 | 405,587 | 8.0x |
| 10,000 | 4,782,563 | 94.7x |

记录数扩大 100 倍，耗时约 94.7 倍，判定开销与记录总数成比例（O(n)）。

## 代码检查

```bash
gofmt -l bitemporal
go vet ./...
```

`gofmt -l` 无输出即格式合规。

## 判定日志的人工复核

随机对照测试把每次判定写入测试临时目录（`go test -v` 输出中可见实际路径）下的
`bitemporal_judgments.jsonl`，每行一条 JSON，字段：

- `source_version` / `target_version` / `record`：判定输入；
- `engine_result`：本组件输出（总体判定 + 拒绝/边界/损失/填充四类轴集合）；
- `naive_reference`：朴素参照模型独立输出；
- `basis`：逐条判定依据（命中了哪条优先级规则、见证点、固定填充规则等）。

快速抽查示例：

```bash
# 统计各总体判定出现次数
grep -o '"verdict":"[a-z_]*"' <日志路径> | sort | uniq -c
```
