# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分裂双端队列（split deque）

见 `splitdeque` 包（`splitdeque/splitdeque.go`、`splitdeque/doc.go`）。

- 三个分裂点 `t ≤ s ≤ b`：`[0,t)` 已窃取，`[t,s)` 为共享区 S（窃取者前端成批取），`[s,b)` 为私有区 P（所有者后端压入/弹出）；`|S|=s-t ≤ Sm`，`|P|=b-s`，占用 `b-t ≤ Cap`。
- 窃取请求：Steal 取 `k=min(m,|S|)` 个（按下标升序复制）。`k<m`（含 0）置 `fl=true`、`ag=0`、`dm=max(dm,m-k)`（首次为 `m-k`）；恰好取满不置请求。
- 释放检查（仅 Push 追加之后、Pop 取元素之前，且仅 `fl` 为真时）：`r = min(max(⌊|P|/2⌋, dm), Sm-|S|, |P|-Rv)`，负数按 0。`r≥1` 则 `s+=r` 并清 `fl/ag/dm`，释放计数 +1；否则 `ag++`，`ag==F` 时请求到期并清 `fl/ag/dm`。返回空的 Pop 同样执行检查、累计年龄。
- 回收：Pop 时私有区为空而共享区非空，先令 `g=⌈|S|/2⌉`、`s-=g`（共享区最新 g 个转入私有区），再取 `b-1`，回收计数 +1。
- 释放与回收只移动分裂点、不搬移元素：`MoveCounts()` 返回的前两个计数器恒为 0，第三个等于 Steal 复制的元素总数。
- 恒等式：`Pushed == Popped + Stolen + (b-t)`。

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
