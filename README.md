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

## 空闲状态清理器

`IdleCleaner` 维护全局时钟 `T` 和每个键的 `created`、唯一 `timer`。`Touch(key, now)` 与 `Advance(now)` 都会先把所有满足 `timer <= now` 的键按 `(timer, key)` 升序原子清理，再令 `T=now`；观察者不会看到部分清理状态。

### 保留区间与定时器合并

- 新键：`created=now`，`timer=min(now+Max, now+Hmax)`，登记一次。
- 已存在键且 `now+Min > timer`：新定时器为 `min(now+Max, created+Hmax)`；仅当结果不同于当前定时器时替换并计一次登记。
- `now+Min == timer` 不重登记；因此持续访问时两次必要重登记的间隔为 `Max-Min+1`。
- 新定时器若被绝对寿命截断到与当前值相同，也不增加登记。
- 每个键在堆和键表中始终只对应一个活动定时器。

当 `Hmax` 足够大、某键在每个整数时刻都被访问时，截止到 `T` 的登记次数为：

```text
floor(T/(Max-Min+1)) + 1
```

例如 `Min=10, Max=30, T=100000` 时结果为 4762。该次数只统计新建与实际替换；保留现有定时器的空操作不计入非导出字段 `timerOps`。

### 绝对寿命

`Hmax` 是自 `created` 起的硬上限：任何刷新都不能把定时器推迟到 `created+Hmax` 之后。即使持续访问，到达寿命点时键仍会先按定时器触发被移除；若该时刻再次 `Touch` 同名键，则按新键创建并重置 `created=now`。`Hmax` 可以小于 `Max`，此时第一次登记即被寿命截断。

### 容量与拒绝顺序

`Touch` 在任何时钟推进、清理或键表修改之前做容量判定。设 `due` 为当前 `timer <= now` 的键数，目标键不存在于 `timer > now` 的活动集合时，若：

```text
键表大小 - due + 1 > K
```

则返回 `ErrCapacityExceeded`。公式把本次操作即将释放的到期槽位计为可用，因此“先到期、再新建”可通过；判定失败则时钟、清理结果、键表和所有计数保持不变。

错误只返回第一个可区分原因：

- `Touch`：`ErrInvalidArgument`（空 key 或 `now` 越界）→ `ErrClockRolledBack`（`now < T`）→ `ErrCapacityExceeded`。
- `Advance`：`ErrInvalidArgument` → `ErrClockRolledBack`。
- `Timer` 查询不存在的键返回 `ErrKeyNotFound`。

查询接口包括 `Has`、`Timer`、`Size` 与累计清理数 `Cleaned`。所有方法由同一把读写锁保护，`Touch` 与 `Advance` 均对外呈现为单个原子串行步骤。

### 本地验证

```bash
# 若 PATH 中没有 go，可将 go 替换为 /usr/local/go/bin/go；只读 HOME 时指定 GOCACHE
GOCACHE=/tmp/ontology-go-cache go test -v ./...
GOCACHE=/tmp/ontology-go-cache go test -race ./...
GOCACHE=/tmp/ontology-go-cache go vet ./...
gofmt -l .
```

测试覆盖定时器等于/早于 `now` 一毫秒边界、`now+Min == timer` 与大 1 的差异、寿命截断和同值不登记、寿命到期后重建、容量先判后清、空 `Advance` 与回退、同刻按键序清理、4762 次登记公式，以及 2000 组随机序列与全表扫描朴素模型对照。随机测试在失败日志中打印每个操作的输入、输出和判定依据。
