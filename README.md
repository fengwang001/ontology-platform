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

## nsec：DNSSEC 聚合 NSEC 否定应答缓存

`nsec` 包把已验证的 NSEC 记录按规范序缓存成区间，并据此直接合成
NXDOMAIN 与 NODATA 否定应答。构造：`nsec.New(zone, soaMin, capacity)`，
其中 `zone` 为区域顶点（非空域名），`soaMin` 为 SOA 否定上限
（0..86400 秒），`capacity` 为容量 N（1..4096）。

### 规范序与覆盖判定

- 域名拆成标签后从最右一个标签起逐个比较；标签按 ASCII 小写字节逐字节
  比较，较短且为前缀者更小；所有公共标签相同时标签更少者更小（祖先先于
  后代）。例如 `a.example < z.a.example < b.example`。名字解析大小写不
  敏感（折叠为小写），允许单个末尾 `.`，标签为 1..63 字节的
  字母数字与 `- _ *`，整体不超过 253 字符。
- 记录 `Owner -> Next` 覆盖名字 `x`：当 `Owner < Next` 时要求
  `Owner < x < Next`；当 `Owner >= Next`（链尾回绕，含相等）时要求
  `x > Owner` 或 `x < Next`。多条存活记录同时覆盖时取 Owner 规范序最大
  者。缓存内部按 Owner 规范序维护有序切片，覆盖判定经二分查找完成，
  单次 Lookup 的名字比较次数不超过 `4*(ceil(log2(n+1))+2)`（区间两两不
  重叠、回绕记录至多一条时；重叠时结果仍然正确，仅扫描变慢）。

### 最近封闭者与通配符否定

`Lookup(now, qname, qtype)` 依次判定：

1. 存在 Owner 等于 qname 的存活记录：Types 含 qtype（或 qtype 非 5 且
   Types 含 CNAME=5，别名）时为 Miss，否则为 NoData。
2. 否则找覆盖 qname 的存活记录 R，没有则为 Miss。
3. 令 `k` 为 `R.Owner`、`R.Next` 与 qname 的公共后缀标签数的较大者；
   若 `k` 等于 qname 的标签数，qname 是空非终结符（ENT），结果为
   NoData。
4. 否则最近封闭者 `ce` 为 qname 右起 k 个标签，通配符名为 `*.ce`：
   若存在 Owner 等于该通配符名的存活记录，或没有存活记录覆盖它，结果
   为 Miss；否则为 NXDomain。Used 为 R 与覆盖通配符的记录（同一条只列
   一次），按 Owner 规范序升序；结果 TTL 取 Used 各记录剩余秒数
   （到期时刻 − now）的最小值。

### 有效期与到期边界

- 每条记录的有效期 `eff = min(TTL, soaMin)`，到期时刻为 `now + eff`；
  当且仅当 `now < 到期时刻` 时存活（恰等即过期）。
- `eff` 为 0 时不存储（但仍清除同 Owner 的旧记录）。
- 过期记录在每次被接受操作的开头按到期时刻经最小堆摊还清除，每条记录
  至多被清除一次；Lookup 不改动任何记录内容。

### 淘汰规则

Insert 先清除同 Owner 的旧记录；当存活记录数已达 N 且 Owner 为新名时，
先淘汰到期时刻最小的存活记录（并列取 Owner 规范序较小者）再加入。
错误优先级：参数非法 > 时钟回退（now 小于上次被接受操作的 now）>
ErrNotValidated（仅 Insert）> ErrOutOfZone；被拒绝的操作不改变任何
记录与时钟。所有方法可并发调用，效果等价于某个串行顺序。

### 本地验证

```bash
# 全部测试（含规范示例、2000 组随机序列与朴素模拟对照、
# n=100/10000 的比较次数断言、并发竞态）
go test ./nsec/
go test -race ./nsec/

# 查看随机对照日志（输入、输出与判定依据）
go test ./nsec/ -run TestRandomAgainstSim -v

# 查看比较次数实测值
go test ./nsec/ -run TestNameCmpBound -v
```
