# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## retention：阶梯式历史版本保留清理器

`retention` 包按文件维护带时间戳与大小的历史版本，并按年龄档位逐步稀疏化，
同时受固定版本、条数与字节上限约束。每次 `Clean` 返回被删版本及原因
（`Aged` / `Thinned` / `OverCount` / `OverBytes`），结果可精确复现，
且与清理发生的历史有关（增量清理与一次性清理结果可能不同）。

### 档位与稀疏化

- 构造参数 `rules` 为非空的 `Rule{Until, Step}` 列表，`Until` 严格递增且 ≥1，
  `Step` ≥1（各档 `Step` 不要求单调）。最后一档的 `Until` 即最大年龄。
- 版本年龄 `a = now - t`，所属档位为满足 `a < Until` 的最小下标
  （`a` 恰等于某档 `Until` 时落入下一档）；`a` ≥ 最后一档 `Until` 为超龄。
- `Clean(now)` 对每个文件依次执行三步：
  1. **Aged**：删除未固定、非最新、超龄的版本；
  2. **Thinned**：按 `t` 升序扫描，维护“上一个保留的版本” `prev`；
     固定版本、最新版本、首个版本直接保留并成为 `prev`；其余版本若
     `t - prev.t < 所属档位 Step` 则删除（恰等于 `Step` 保留），否则保留并成为 `prev`；
  3. **上限**：当条数 > `maxCount` 或字节和 > `maxBytes` 时，反复删除
     “最老的未固定且非最新”版本；每次删除时若条数超限记 `OverCount`，
     否则记 `OverBytes`。固定与最新版本永不被删，即使因此仍然超限。
- 每个文件中 `t` 最大的现役版本为“最新”，其身份随 `Add` / 清理而变化。

### 固定（Pin）语义

- `Pin` / `Unpin` 幂等；对不存在或已在回收站的版本报 `ErrNoVersion`。
- 固定版本在三个阶段都不会被删，并且会作为 Thinned 阶段的间隔基准
  （`prev`），从而改变后续版本的存活结果；该影响不可撤销——
  之后即使 `Unpin`，已被删的版本也不会自动回来。

### 回收站

- 被 `Clean` 删除的版本进入回收站并记 `deletedAt = now`；
  `now - deletedAt ≥ trashTTL` 的条目在下一次 `Clean` 时被彻底清掉（`Purged`）。
- `Undelete(now, file, t)` 在 TTL 内可恢复版本（恢复后固定标记清除，
  下一次 `Clean` 照常判定）；已过期（即使尚未被清掉）或不存在报 `ErrNoVersion`。
- `Trash(file)` 返回回收站全部条目（按 `t` 升序）。

### 一致性与错误

- 所有方法可并发调用，效果等价于某个串行顺序；被拒绝的调用不改状态；
  相同调用序列重放得到完全相同的返回；同一 `now` 重复 `Clean` 幂等。
- `now` 必须单调不减，否则 `ErrClock`。`Add` 错误优先级：
  `ErrClock` > `ErrInvalid` > `ErrDuplicate` > `ErrTooLarge`
  （同一 `(file, t)` 已存在即重复，含回收站中未彻底清掉的条目；
  现役字节和加 `size` 超过 10^15 报 `ErrTooLarge`）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
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

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 本地验证 retention

```bash
# 规则单测（档位边界、固定语义、上限次序、回收站 TTL 边界等）
go test ./retention/

# 2000 组随机调用序列与朴素实现对照，-v 打印每步输入、输出与判定依据
go test -run TestRandomSequencesAgainstNaive -v ./retention/

# 并发等价性（竞态检测）
go test -race ./retention/
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
