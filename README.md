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

## Kerberos 票据簿

实现位于 `kerberos` 包，构造参数为最大寿命 `L`、最大可续期时长 `R`、时钟偏差 `S` 和最大后置时长 `P`。合法条件为整数秒且 `1 <= L <= R <= 10^9`、`1 <= S,P <= 10^9`。所有时间为 `[0,10^15]` 内的整数秒，所有被接受操作共用单调全局时钟；`now` 小于此前接受操作的最大 `now` 时拒绝为时钟回退。

### 票据时间推导

- TGT：`start=0` 时取 `s=now`，否则要求 `s=start >= now`，且 `s <= now+P`；要求 `till > s`；`end=min(till,s+L)`。
- TGT 续期上限：`renewTill>0` 时先取 `rt=min(renewTill,s+R)`；仅当 `rt>end` 时保留，否则票据的 `renewTill=0`。
- 后置 TGT：`s>now` 时签发成功但 `invalid=true`；只有 `Validate` 在 `start <= now < end` 成功后才可用于 TGS 等操作。
- 服务票据：TGS 要求 TGT 已生效、未置无效、未过期且密钥未更换，且 `till>now`；服务票据 `start=now`，`end=min(till,now+L,TGT.end)`。
- 服务票据续期上限：当 TGT 可续期且请求 `renewTill>0` 时，`rt=min(renewTill,now+R,TGT.renewTill)`；仅当 `rt>end` 时保留。服务票据的 `end` 与 `renewTill` 在签发时快照，之后不随 TGT 续期改变。
- 续期：仅对当前已生效、未置无效、未过期、密钥未更换且 `renewTill>0` 的票据执行；取当前寿命 `end-start`，新窗口为 `[now,min(now+(end-start),renewTill))`。若新结束时刻不大于当前 `end`，拒绝为续期上限；续期不改变 `renewTill` 与 `issued`。
- 有效性采用左闭右开：`start <= t < end`；因此 `t==end` 为已过期。

### 固定判定次序

所有操作先查参数非法，再查时钟回退，再查票据不存在；TGS 中编号存在但不是 TGT 时报非 TGT。

- `IssueTGT`：参数非法、时钟回退、区间非法、后置过远。
- `TGS`：参数非法、时钟回退、票据不存在/非 TGT、尚未生效、无效标志、已过期、密钥已更换、区间非法。
- `Renew`：参数非法、时钟回退、票据不存在、尚未生效、无效标志、已过期、密钥已更换、不可续期、续期上限。
- `Validate`：参数非法、时钟回退、票据不存在、非后置票据、尚未生效、已过期、密钥已更换。
- `Authenticate`：参数非法、时钟回退、票据不存在、时钟偏差、尚未生效、无效标志、已过期、密钥已更换、重放。

被拒绝的操作不会推进时钟，也不会修改票据、编号计数、密钥版本或重放缓存。`ChangeKey(subject,now)` 记录主体当前密钥版本；票据以签发动作的 `issued` 判断，服务票据继承 TGT 主体。若 `issued < c` 则票据失效，`issued == c` 不失效；续期与验证都不刷新 `issued`。

### 重放缓存

只有通过全部认证校验的 `Authenticate` 才写入 `(ticketID,authTime)`。缓存项逻辑保留到 `now > authTime+S` 为止；在 `now == authTime+S` 时仍可判为重放，大 1 秒时视同不存在并会先触发时钟偏差。内部使用哈希表判定重放，用配对堆按 `authTime+S` 延迟回收：哈希查询/登入保持 `O(1)`，堆弹出在多次被接受操作间摊还；非导出计数器记录回收弹出次数，每次清理实际弹出的只是本次到期项。

并发调用由单一互斥保护，整体效果等价于某个串行顺序；同一 `(ticketID,authTime)` 的并发认证恰有一个成功。

### 本地验证

```bash
# 若环境未将 Go 放入 PATH，可直接使用 /usr/local/go/bin/go；如下命令等价。
go test ./...
go test -race ./...

# 查看 2000 组随机序列的输入、输出与判定依据
go test ./kerberos -run 'TestRandomAgainstNaiveSimulation/seed=1$' -v -count=1

# 代码格式与静态检查
gofmt -w kerberos/*.go
go vet ./...
```
