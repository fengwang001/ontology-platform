# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Omid 式有界冲突检测表

实现位于 `ontology/manager.go`，由 `NewCommitManager(B, A)` 创建固定容量的快照隔离提交管理器：

- `B` 为桶数，范围 `1..1024`；`A` 为每桶容量，范围 `1..16`，越界返回 `ErrInvalidConfiguration`。
- `Begin()` 将全局计数 `n` 加一并把该值作为事务号加入活跃集合；成功提交使用同一个计数器生成 `c=n+1`，因此事务号与提交时间戳互不重复。
- `Commit(s, keys)` 先对输入去重得到 `W`。`W` 为空时直接成功、返回 `0`，不查询哈希表、不分配时间戳、不受水位影响，但事务会离开活跃集合。
- 非空提交先检查所有写集键：仅查看 `key mod B` 所在桶；若表项时间戳大于 `s`，返回冲突中止。冲突检查全部通过后才检查淘汰水位 `Tm > s`。
- 通过检查后先分配提交时间戳并让提交者离开活跃集合，再计算剩余活跃事务号最小值 `m`；没有剩余活跃事务时 `m=+∞`。
- 按键升序登记。已有表项只把时间戳更新为 `c`，不占用新槽位。新键遇到满桶时，先清除桶内时间戳不大于 `m` 的表项；这些表项不可能再与当前活跃事务冲突，因此不抬高 `Tm`。
- 清除后仍满时，淘汰时间戳最小的表项；时间戳并列时淘汰键较小者，并执行 `Tm=max(Tm, 被淘汰时间戳)`，随后插入新表项。
- 水位中止是保守中止：它只能证明被淘汰历史可能遗漏冲突，并不保证当前写集确实与已提交事务冲突；冲突检查和水位检查之间严格只报告先发生的冲突。
- 冲突、水位中止和主动 `Abort(s)` 都会让事务离开活跃集合，但不改变 `n`、哈希表和 `Tm`；无效事务、超过 64 个输入键、键越界属于调用拒绝，状态完全不变且事务保持活跃。
- 所有方法由互斥保护，可并发调用；返回结果等价于某种串行顺序。`Snapshot()` 导出时间戳、水位、活跃事务和按键排序的表项用于测试与可复现核对。

相同调用序列确定性地产生相同结果和表内容。测试中的朴素模拟器使用线性扫描逐字复刻同一规则，2000 组固定种子随机序列会同时比较哈希实现结果、表内容、桶容量、全历史冲突和探测次数。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 如果 go 不在 PATH 中，可使用 /usr/local/go/bin/go
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 将随机对照的输入、输出和判定依据写入日志文件（下面只跑 seed=1）
ONTOLOGY_VERBOSE_RANDOM=1 go test -v -run 'TestRandomSequencesMatchNaiveModel/seed=1-' ./ontology > random-commit-test.log

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
