# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## window 包：带清理与复活语义的窗口状态管理器

`window.Manager` 按窗口累计事件值并在越过阈值时触发，支持清理、迟到复活与回收，全部方法可并发调用。

### 状态迁移

```
(不存在) --首个普通事件--> Active        （隐式创建窗口）
Active   --Cleanup-------> Cleaned       （累计值冻结保留，不再参与累计）
Cleaned  --LateEvent-----> Revived       （复活：累计值只取这一条迟到事件的值，丢弃冻结历史，不触发）
Cleaned  --Reap----------> (删除)         （回收）
Revived  --任何事件-------> Revived       （照常累加，但不再触发；回收时保留）
```

- 触发规则：仅 `Active` 窗口在累计值从下往上越过阈值的整数倍时触发，每越过一个倍数触发次数加一并产出一条 `TriggerEvent`；单条事件连续越过多个倍数则产出多条。
- 已清理窗口收到普通事件会被拒绝（`ErrWindowCleaned`），只能由迟到事件复活。

### 复活与回收互斥

复活（`LateEvent` 作用于 `Cleaned` 窗口）与回收（`Reap`）由同一把互斥锁串行化：
对一个已清理未复活的窗口，要么回收先获得锁将其删除（之后的迟到事件因窗口不存在返回 `ErrWindowNotFound`），
要么复活先获得锁将其置为 `Revived`（之后的回收跳过已复活窗口）。二者不可同时发生。

### 可判定错误

四类非法输入对应互不相同的哨兵错误，可用 `errors.Is` 判定，且失败不改变任何状态：

| 场景 | 错误 |
| --- | --- |
| 标识非法（空或仅空白） | `ErrInvalidID` |
| 值非法（非正数） | `ErrInvalidValue` |
| 对从未创建过（或已被回收）的窗口发迟到事件 | `ErrWindowNotFound` |
| 窗口数超上限 | `ErrTooManyWindows` |

### 本地验证

```bash
# 全部测试（含竞态检测与输入/结果/判定依据日志）
go test -race -v ./window/

# 静态检查
gofmt -l .
go vet ./...
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

## 代码检查

```bash
gofmt -l .
go vet ./...
```
