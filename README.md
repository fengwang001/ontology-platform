# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 阶梯式历史版本保留清理器（`ontology` 包）

`ontology.Cleaner` 为每个文件保存带时间戳 `t` 与大小 `size` 的历史版本，
按版本年龄所在档位的最小间隔逐步稀疏化，并受固定版本、条数、字节上限约束。
每次清理删除哪些版本及原因可精确复现，且结果依赖清理发生的历史时刻。

### 构造参数

`New(rules, maxCount, maxBytes, trashTTL)`：

- `rules`：非空的档位列表，每档 `Rule{Until, Step}`，`Until` 严格递增且
  `≥1`，`Step ≥1`；各档 `Step` 不要求单调（这正是“增量清理”与“一次性
  清理”结果可能不同的来源）。
- `maxCount`、`maxBytes`：每个文件现役版本的条数与字节和上限，均 `≥1`。
- `trashTTL`：被清理版本在回收站中可恢复的时长，`≥1`。

### 档位与稀疏化规则

- 版本年龄 `a = now - t`。档位是满足 `a < Until` 的最小下标；年龄恰等于
  某档 `Until` 属于下一档；`a ≥ 最后一档 Until` 视为超龄。
- `Clean(now)` 先清回收站：`now - deletedAt ≥ trashTTL` 的条目被彻底清除，
  计入 `CleanResult.Purged`，按 `(file, t)` 升序。
- 然后对每个文件独立执行三步，新删除的版本以 `deletedAt = now` 进入回收
  站并计入 `CleanResult.Deleted`（按 `file` 字节序、再按 `t` 升序）：
  1. **Aged（超龄）**：未固定、非最新且超龄的版本删除。
  2. **Thinned（稀疏化）**：按 `t` 升序扫描剩余版本，维护“上一个保留的
     版本” `prev`。版本若已固定、是最新、或尚无 `prev` 则保留并成为
     `prev`；否则取其所在档位的 `Step`，`t - prev.t < Step` 删除，
     `≥ Step`（恰等于也）保留。每个版本在本步恰被考察一次。
  3. **上限**：现役条数 `> maxCount` 或字节和 `> maxBytes` 时，反复删除
     “最老的未固定且非最新”版本，直到两个条件都满足或没有可删版本。
     每次删除的原因按删除当时判定：条数仍超限为 `over_count`，否则为
     `over_bytes`。

### 固定版本（Pin）语义

- 每个文件 `t` 最大的版本恒为“最新”，永远不会被任何一步删除。
- `Pin(file, t)` 固定的版本同样永不被删，并在稀疏化时强制成为新的间隔
  基准（后续版本以它为 `prev`），因此固定会改变后续版本的存活结果。
- 固定/取消固定对不存在或已在回收站的版本报 `ErrNoVersion`；重复
  Pin/Unpin 幂等成功。
- 因固定而改变基准后被稀疏掉的版本，事后即使取消固定也不会自动恢复，
  只能在回收站 TTL 内通过 `Undelete` 找回；恢复后固定标记被清除。

### 时钟、回收站与错误优先级

- `now` 在 `Add`/`Clean`/`Undelete` 中必须不小于此前任一次调用，否则
  `ErrClock` 且不改状态。
- `Add` 错误优先级：`ErrClock` > 参数非法 > `ErrDuplicate` >
  `ErrTooLarge`。同一文件同一 `t` 在现役或回收站（含已过期但尚未被
  Clean 彻底清掉）中已存在即 `ErrDuplicate`。
- 单文件现役字节和（含本次 `size`）超过 `10^15` 时 `Add` 报
  `ErrTooLarge`（以 `size > 10^15 - sum` 判定避免溢出）；`Undelete` 不做
  此判定。
- `Undelete` 在回收站无条目或条目已到期（哪怕还没被下一次 Clean 清掉）
  时报 `ErrNoVersion`。`Trash(file)` 返回回收站全部 `(t, size,
  deletedAt)`，按 `t` 升序。

### 复现性与并发

- 所有方法由互斥锁保护，并发调用等价于某个串行顺序；被拒绝的调用不改
  变状态；相同调用序列重放得到完全相同的返回。
- 同一 `now` 重复 `Clean`：第二次不删除、不清除任何条目（幂等）。
- 由于档位 `Step` 可随年龄变化，先在“大间隔档位”被稀疏掉的版本，与等
  到它进入“小间隔档位”才一次性 `Clean` 的结果可能不同。测试
  `TestIncrementalVersusOneShot` 给出了这一构造。

### API 速览

```go
c, err := ontology.New(
    []ontology.Rule{{Until: 60, Step: 20}, {Until: 300, Step: 100}},
    50,        // maxCount
    1<<30,     // maxBytes
    24*3600,   // trashTTL
)
err = c.Add(now, "file-a", t, size)
res := c.Clean(now)          // res.Purged / res.Deleted（含 Reason）
_ = c.Pin("file-a", t)
err = c.Undelete(now, "file-a", t)
vs := c.Versions("file-a")   // 现役版本，按 t 升序
n, b := c.Totals("file-a")
ts := c.Trash("file-a")
```

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

`ontology` 包的验证要点：

- 定向用例覆盖档位边界（年龄恰等 `Until`、间隔恰等 `Step`）、最新版本
  身份随时间变化、固定改变间隔基准、固定抗超龄、条数/字节上限删除次序与
  `over_count`/`over_bytes` 判定、超限但固定/最新仍保留、回收站 TTL
  边界前后恢复、已过期但未清掉的条目语义、增量与一次性清理差异、时钟倒
  退与错误优先级、确定性重放。
- `TestRandomDifferential` 生成 2000 组随机调用序列（每组 60 个操作，
  `Step` 允许非单调），与按规范逐步誊写的朴素实现逐操作对照返回值、现役
  集合、回收站、条数/字节与非导出的稀疏化考察计数。
- `TestRandomDifferentialLogSample` 在 `-v` 下打印每条操作的输入、输出
  与判定依据：

  ```bash
  go test -run TestRandomDifferentialLogSample -v ./ontology
  ```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
