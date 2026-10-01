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

## scoreboard：基于选择确认的发送端记分板

`scoreboard` 包（`ontology/scoreboard`）记录已发数据段、累计确认与选择确认（SACK）区间，判定丢失段并按最小起点优先重传。

### 模型

- 序号为字节偏移，从 0 起；`Send(length)` 按序追加段并返回起点，长度须在 `1..M`。
- 段边界 = 某段起点或已发送末端。
- `Ack(cum, blocks)` 记录累计点 `cum`（小于 `cum` 的字节全部收到）与若干半开 SACK 块；块可重叠或相邻，按并集合并记录；累计点越过的段与 SACK 记录随之清除。
- 所有方法可并发调用（内部互斥锁）；被拒绝的调用整体生效、不改变任何状态；相同调用序列重放结果完全相同。

### 判丢条件

一个未确认且未被选择确认的段被判丢失，当且仅当满足二者之一：

1. 高于它的已选择确认区间中，不相交的连续片段数 **≥ D**；或
2. 高于它的已选择确认字节数 **≥ (D−1)·M**。

### 在途字节

在途字节 = 全部未确认且未被选择确认的段中，「未判丢失或已重传」者的长度之和。每次状态变更后都按此定义对当前记录整体重算，因此任何时刻 `Inflight()` 都等于重算值，不因调用交错而变。

### 重传

`Retransmit()` 取起点最小的已判丢失且未重传的段，仅当「在途字节 + 该段长度 ≤ W」时允许（恰好等于 W 也可以），成功后标记已重传。拒绝原因可区分：无可重传段返回 `ErrNoRetransmittable`（优先判定），在途已满返回 `ErrInflightFull`。

### 本地验证

```bash
# 单元测试（日志打印输入、输出与判定依据）
go test -v ./scoreboard

# 并发与竞态检测
go test -race -v ./scoreboard

# 静态检查
gofmt -l .
go vet ./...
```
