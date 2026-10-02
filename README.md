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

# 批写入隔离器（二分隔离 + 已知毒丸表）
go test -race -v ./batchisolate/
go test -run TestNaiveSimRandom2000 -v ./batchisolate/  # 2000 组随机对照
go test -run TestBound1024 -v ./batchisolate/           # 调用次数 21/11 与上界

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 批写入隔离器（`batchisolate`）

`batchisolate` 包对整批原子写入的 sink 做失败隔离：瞬时错误在 R+1 次内
重试；永久失败的批按左半 ⌈n/2⌉ 二分定位毒丸，右半在「本批永久失败且左半
成功」时直接推断含毒丸、跳过整批调用。毒丸入已知毒丸表（FIFO、容量 Km、
Submit 开始读快照/结束整体并入，并发互不可见）；预算 Cmax 耗尽时未裁决
记录记 `Budget`。无瞬时、无已知、k≥1 个毒丸时 sink 调用次数满足
`calls ≤ 1+2·k·⌈log₂n⌉`（n=1024 最左 21、最右 11）。

详见 `batchisolate/DESIGN.md`。
