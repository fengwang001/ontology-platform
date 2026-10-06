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

## exam 包：在线考试会话引擎

断线续考与防作弊窗口引擎，见 [DESIGN.md](DESIGN.md)。

- 服务端单调整数时钟驱动：作答预算、绝对截止、暂停预算、单次暂停上限。
- 断线暂停/凭证续考：凭证仅对最近一次暂停有效，续考签发新会话代次。
- 作答记录按会话内序号落定：乱序取最大、迟到拒绝、重复幂等。
- 异常事件滑动窗口（左开右闭）：警告、锁定、违规结束分级处置。
- 结算给出每题最终作答、有效作答时长、暂停总时长、警告次数与违规标记。

```bash
go test ./exam/          # 场景测试 + 朴素模型随机对照
go test -race ./exam/    # 竞态检测
go test -v ./exam/ -run TestRandomizedAgainstModel  # 打印每步判定依据
go test ./exam/ -bench . -run XXX                   # O(1) 判定基准
```
