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

## lockmgr：基于年龄的死锁预防锁管理器

`lockmgr` 包实现 wound-wait 死锁预防方案，事务可申请键的独占锁，无需构造等待图。

### 伤害与排队规则

- 事务 `Begin` 时获得从 1 起连续递增的年龄号，号小者更老。
- `Acquire` 申请键的独占锁：
  - 键空闲 → 直接授予；持有者就是自己 → 视为已持有，不改变任何状态。
  - 持有者比申请者**年轻** → **伤害**持有者：其全部锁立即释放、在任何键上的排队请求一并撤销、状态变为已伤害（记录伤害者年龄号），随后本键授予申请者。
  - 持有者比申请者**老** → 申请者进入该键的等待队列。
- 任何锁被释放（提交、中止或被伤害）时，队列中**年龄号最小**的等待者获得该键并回到活跃，其余继续等待。
- 已伤害事务只能 `Restart`：回到活跃、**年龄号不变**、不持有任何锁。
- `Commit` 要求事务活跃；`Abort` 在活跃、等待、已伤害时都允许，并释放全部锁与排队请求。
- 申请与提交的非法情形按固定顺序只报第一个：键为空 → 事务不存在 → 已提交或已中止 → 已被伤害 → 正在等待；重启非已伤害事务有独立拒绝原因。被拒绝的操作不改变任何状态。

### 为何无等待环

等待边只会从年轻事务指向年老事务（年轻者排队等老持有者；老事务的申请绝不排队，而是直接伤害年轻持有者）。年龄号是严格全序，沿等待边年龄严格递减，不可能回到起点，因此等待图恒为无环，系统不会出现死锁，也无需实际构造等待图。

### 重启为何保持年龄号

若重启时分配新年龄号，被伤害事务会变得更年轻，可能反复被更老的事务伤害而饥饿（活锁）。保持年龄号不变后，活跃事务终结得越多，该事务相对越老，最终成为最老事务——此后它只会伤害别人而不再被伤害，保证终能完成。

### 本地验证

```bash
# 全部单测（含老伤年轻、年轻排队、按年龄授予、撤销排队、重启保持年龄号等）
go test -v ./lockmgr/

# 并发随机交错 + 等待关系不变量自检（竞态检测）
go test -race -run TestConcurrentRandomInterleaving -v ./lockmgr/

# 确定性重放
go test -run TestDeterministicReplay -v ./lockmgr/
```

测试日志会打印每个操作的输入、输出与判定依据。
