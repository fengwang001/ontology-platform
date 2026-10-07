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
go test ./ontology/cardinality
go test -run TestConcurrentSingleSlotContention ./ontology/cardinality

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

> 若环境默认 `GOCACHE` 只读，可加 `GOCACHE=/tmp/gocache`。

## 链接关联基数约束（并发名额分配）

`ontology/cardinality` 实现了带基数上限的链接关联创建仲裁，防止并发
"幻影"式基数绕过。核心机制：

- **预留即占位**：`Reserve` 在同一临界区内原子完成"过期回收 → 基线校验 →
  名额计算 → 占位"，进行中请求立即占用名额；
- **三类互斥拒绝原因**（固定判定顺序）：`ReasonBaselineConflict`（基线
  版本落后）> `ReasonCommittedFull`（已确认关联达上限）>
  `ReasonInflightOccupied`（名额被进行中请求暂时性占用）；
- **两阶段终结**：`Commit` 转为已确认关联并推进版本，`Abort` 原子释放
  名额并对新请求立即可见，被拒请求不会被自动重试；
- **中断裁定**：单调时钟租约（`Heartbeat` 续租 / `Sweep` 显式回收 /
  各入口惰性回收），`now == deadline` 即过期，边界无歧义；
- **可验证证据**：每次尝试的到达时刻、计数快照、判定依据与最终结果
  完整记入 `AuditLog`，拒绝路径对约束状态零副作用；
- **串行等价**：判定开销只与当前进行中请求数相关；测试以独立实现的
  朴素全局串行模型做随机脚本逐步对照，并用真实并发审计日志逐步重放。

设计取舍、被放弃方案与边界语义见 `ontology/cardinality/DESIGN.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
