# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 弱硬实时距离优先级调度器

调度器位于 `scheduler` 包。任务字段为：

- `ID`：非空编号，按 UTF-8 字节序列比较，最长 32 字节。
- `Phase`：首次释放时刻 `φ`，范围 `0..1_000_000`。
- `Period`：周期 `T`，范围 `1..1000`，相对截止期等于 `T`。
- `Execution`：执行时间 `C`，满足 `1≤C≤T`。
- `RequiredHits`、`WindowSize`：弱硬实时约束 `(m,k)`，满足 `1≤m≤k≤16`。

### 距离定义

每个任务维护长度为 `k` 的窗口，自旧到新保存最近 `k` 个作业判定结果：`1` 为按时完成，`0` 为未达标。初始窗口为全 `1`。

距离 `s` 是最小的非负整数 `j`，使窗口末尾追加 `j` 个 `0` 并保留最新的 `k` 位后，达标数小于 `m`。若当前达标数已经小于 `m`，则 `s=0`。例如 `(m,k)=(2,3)`：

| 窗口 | `s` | 判定依据 |
| --- | ---: | --- |
| `111` | 2 | 追加两个 `0` 后为 `100` |
| `110` | 1 | 追加一个 `0` 后为 `100` |
| `101` | 1 | 追加一个 `0` 后为 `010` |
| `011` | 2 | 追加两个 `0` 后为 `001` |
| `100` | 0 | 当前达标数已经小于 2 |

当 `m=k` 时，全达标窗口的距离恒为 `1`；窗口内已有任意 `0` 时距离为 `0`。

### 每个 tick 的次序

`Step(n)` 从当前时刻开始连续处理 `n` 个 tick，`n` 必须在 `1..1_000_000`。第 `t` 个 tick 覆盖区间 `[t,t+1)`，开始时刻严格按以下顺序执行：

1. 按编号字节序检查所有未完成作业；若剩余执行量大于绝对截止期余量 `d-t`（包括 `d=t` 且尚未完成），立即判为未达标，追加 `0` 并丢弃该作业。
2. 释放满足 `t≥φ` 且 `(t-φ)%T=0` 的新作业，剩余执行量置为 `C`，绝对截止期为 `d=t+T`。
3. 在仍有作业的任务中，按 `(s,d,ID)` 的字典序选择最小者运行一个 tick；这里的 `s` 使用第一、二步追加后的窗口。
4. 若运行后剩余量归零，追加 `1` 并移除作业；完成时刻为 `t+1`，因此恰等于 `d` 时仍为达标。

剩余量等于 `d-t` 时不丢弃，比它大 `1` 时立即丢弃。未达标在可证明无法按时完成的 tick 立即入窗，不等到截止期；被丢弃作业不占后续 tick。每次追加后都单独检查窗口达标数是否小于 `m`，即使上一次检查已经低于 `m`，本次仍会增加动态失败次数。

### API 与错误

- `New()`：创建调度器。
- `AddTask(Task)`：只能在第一次 `Step` 前调用；拒绝顺序为已开始、参数非法、编号重复、任务数已满（16 个）。
- `Step(n)`：推进 `n` 个 tick。
- `Window(id)`、`Distance(id)`、`Stats(id)`：查询窗口副本、当前距离与 `{达标数, 未达标数, 动态失败次数}`。
- `RunAt(t)`：返回第 `t` 个 tick 运行的任务编号；空闲或 `t` 尚未执行时返回空字符串。

所有操作由互斥锁保护，并发调用等价于某个合法串行顺序。`Step(a+b)` 与先 `Step(a)` 再 `Step(b)` 的轨迹、窗口和计数逐位一致。

### 本地验证

```bash
# 全量测试
GOCACHE=/tmp/go-cache-ontology go test ./scheduler -v

# 竞态检测
GOCACHE=/tmp/go-cache-ontology go test -race ./scheduler

# 2000 组随机任务集与逐步朴素模拟器对拍（详细日志打印输入、输出和判定依据）
GOCACHE=/tmp/go-cache-ontology go test ./scheduler -run TestRandomDifferential2000 -v

gofmt -w scheduler/*.go
GOCACHE=/tmp/go-cache-ontology go vet ./...
```
