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

## 分区提交触发器（partitioncommit）

`partitioncommit` 包实现按事件时间划分的分区提交触发器，所有方法可并发调用，
效果等价于某个串行顺序（内部以互斥锁串行化）。

### 就绪判定

- 分区 `k` 覆盖事件时间 `[k*P, (k+1)*P)`；写入携带子任务号（共 `W` 个）与
  非负事件时间，首次写入创建分区并计条数。
- 子任务水位只增不减；全局水位为全部 `W` 个子任务都上报过后的最小值
  （此前不存在）；子任务标记结束后其水位视为无穷大。
- 分区就绪当且仅当全局水位 `>= (k+1)*P + 延迟`（恰等边界即就绪）。

### 提交与补提交

- 首轮提交依次执行「登记元数据」「写成功标记」两步；每完成一轮，提交版本加一。
- 各步由外部对当前可执行步骤上报成功或失败；失败使该步失败次数加一，
  累计达 `R` 次分区转「提交失败」；人工重置后回到就绪、从该轮第一步重来
  且失败次数清零。
- 已提交分区再收到写入时条数增加并转「待补提交」，该轮只有「写成功标记」
  一步；补提交完成前的多次写入合并在同一轮。
- 分区尚未提交完成（含提交失败）时的写入只增加条数、状态不变。

### 阻塞次序

- 分区每轮的第一步只能在所有编号更小的已创建分区都处于「已提交」或
  「待补提交」状态时开始；进行中或提交失败的更小分区会阻塞它，
  保证提交按分区编号升序推进。

### 本地验证

```bash
# 运行触发器测试（日志打印各操作的输入、输出与判定依据）
go test -v ./partitioncommit

# 带竞态检测
go test -race ./partitioncommit
```
