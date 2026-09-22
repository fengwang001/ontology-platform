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

## 两阶段提交协调者

三个包，依赖方向单向：`vote`（投票收集与裁决，不依赖其他包）←
`participant`（参与者状态机与幂等指令）← `coord`（协调者，注入时钟驱动两阶段）。

```bash
go run ./cmd/demo   # 逐项演练九条语义，全部 OK 且退出码为 0
```

关键语义：

- 全票同意才提交；任一否决或超时全体中止，已同意者回滚到已中止态。
- 截止判定按注入时钟、左闭右开：`now == deadline` 即算超时；中止原因可区分
  「被否决」与「超时」，并指出具体参与者。
- 决议一次写定，迟到的票与重复驱动都不改写；提交/中止指令幂等，
  本地提交动作计数只增一次。
- **空参与者集合的决议为 Commit**：空集上「全票同意」vacuously 成立，
  且没有任何参与者需要中止，提交是唯一确定且无副作用的结果。
