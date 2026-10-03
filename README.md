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

## 实验层流量编排器（`traffic` 包）

`traffic/` 实现带冷却隔离与世代重盐的互斥实验层流量编排器：为各实验
分配/扩缩/回收连续桶区间，并把用户哈希映射到所属实验。完整规则见
`traffic/doc.go`。

- 桶三态：空闲、占用、冷却；冷却桶记录上一任所有者 `o`、释放时刻 `r`
  与冷却次数 `k`。对 `o` 本人始终可用；对他人在
  `now >= r + Cd*min(k,3)` 时才可用（恰等可用；`Cd=0` 释放即可用）。
- `Claim` 取起点最小的整段可用区间；`Resize` 缩小只冷却尾部、扩大只向右
  原地延伸不搬迁；`Release` 全部进入冷却；同 id 之后可重新 `Claim`。
- `Reshuffle` 使世代 `g+1`、清空全部冷却桶、所有桶 `k` 清零（占用不变）。
- 映射公式：`bucket = (h + g*P) mod B`；桶号 `< H` 为永久对照组。
- 拒绝原因按固定优先级返回：参数非法 → 已存在 → 不存在 → 时钟回退 →
  世代耗尽 → 容量不足；被拒绝操作不改任何状态。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./traffic/

# 朴素模拟对照（2000 组随机序列，逐步比对桶表/世代/Usage/Lookup，
# 日志打印每组参数、每步输入/输出/判定依据与结果直方图）
go test -v -run TestRandomAgainstNaive ./traffic/

# 单遍扫描预算（非导出计数器，B=100 与 10000 两档）、确定性重放、并发安全
go test -race -v -run 'TestScanBudget|TestDeterministicReplay|TestConcurrentSerializability' ./traffic/
```
