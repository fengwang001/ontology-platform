# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 广播状态规则版本化（`broadcast` 包）

`broadcast` 实现规则的“发布 / 投递”两阶段广播：规则先在全局发布，再按
版本逐个投递到处理实例。每条数据按其**到达那一刻**的全局已发布版本打
标签，处理结果只取决于该版本的规则快照，与各实例收到投递的先后无关，
因此任意投递穿插方式都可复现出同一份命中集合。

### 生命周期

- **发布 `Publish(rules)`**：入参为一版全量规则快照；全局版本从 0 开始
  每次加一。新版本不会立即作用于任何实例；快照中未包含的规则视为删除。
  规则命中条件为 `value >= Threshold`。
- **投递 `Deliver(instance, v)`**：实例必须严格按 `1、2、3…` 的顺序应用
  版本，允许跨版本追赶（实例版本为 0 时可直接应用版本 1、再应用版本 2，
  标签为中间版本的数据同样会在对应版本应用时刷出）。
- **发送 `Send(key, value)`**：数据按 `key % N` 路由到唯一实例，并打上
  当前全局版本标签。实例已生效版本等于标签时立即处理；否则进入该实例的
  FIFO 缓冲区。
- **缓冲刷出**：实例每应用完一个版本，立即按到达顺序处理缓冲中所有标签
  恰好等于该版本的数据，然后才允许应用下一版本。一条数据对每条命中的
  阈值规则各输出一条 `Hit`，多条命中按规则标识升序排列。
- **查询 `Hits()`**：返回命中的有序快照（同一实例内严格按数据到达顺序，
  每条数据内部按规则标识排序）。

### 并发与一致性

`Publish` / `Deliver` / `Send` / `Hits` 等全部接口可被多个执行体并发
调用，内部以单把互斥锁串行化状态变更与读取。对于同一串“发布 + 发送”
序列，无论投递如何穿插、与发送如何并发，最终命中集合相同、同一实例上
的相对顺序相同（同批并发发送之间的先后由调度器决定，不属于承诺范围）。

### 边界与错误类别

所有非法输入在任何状态变更之前校验，**整体拒绝、失败不留痕**（全局版本、
各实例版本、缓冲区、已有命中均不变），且返回互不相同的哨兵错误，可用
`errors.Is` 区分：

- `ErrInvalidRule`：规则标识为空，或同一版本内规则标识重复。
- `ErrInstanceOutOfRange`：投递的实例编号越界。
- `ErrVersionOutOfRange`：投递版本不是已发布版本，或未按“当前版本 + 1”
  的顺序投递（跳号、重复投递）。
- `ErrNegativeKey`：数据键为负。
- `ErrBufferFull`：数据需要缓冲但目标实例缓冲区已满。

### 日志

`NewEngine(n, cap, broadcast.WithLogger(w))` 可注入日志输出，逐步记录
每次发布 / 投递 / 发送 / 查询的输入、全局与实例版本、缓冲区占用以及
“立即处理 / 进入缓冲 / 拒绝原因 / 刷出”等判定依据。

### 本地验证

```bash
# 全部用例（版本跨越、按标签刷出、删除规则、非法输入不留痕、
# 三种投递计划与朴素参照一致、并发投递穿插）
go test -race -v ./broadcast

# 高重复次数压测调度交错
go test -race -count=20 ./broadcast

# 覆盖率与静态检查
go test -cover ./...
go vet ./...
gofmt -l .
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
