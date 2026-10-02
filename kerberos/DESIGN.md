# Kerberos 风格票据簿（`kerberos` 包）

## 1. 配置

`NewKDC(L, R, S, P int64) (*KDC, error)`，所有参数单位为秒：

- `L`：单张票据的最大寿命；`R`：可续期时长上限，要求 `1 ≤ L ≤ R ≤ 10^9`。
- `S`：允许的时钟偏差；`P`：允许的最大后置时长，`1 ≤ S, P ≤ 10^9`。
- 任一条件不满足返回 `Error{Reason: "config_invalid"}`，不构造对象。
- 所有时间点为 `[0, 10^15]` 的整数秒；全局时钟单调：`now` 小于此前被
  **接受** 操作见过的最大 `now` 即报 `clock_rewind`。被拒绝的操作不推进时钟。

## 2. 票据字段与编号

`Ticket{ID, Subject, Service, IsTGT, Start, End, RenewTill, Invalid, Issued}`：

- 编号 `t1, t2, …`，仅在成功签发时按序递增（TGT 与服务票据共用同一序列）。
- TGT 的 `Service == "krbtgt"`；服务票据服务名非空且不等于 `krbtgt`。
- 有效期左闭右开：时刻 `t` 有效当且仅当 `Start ≤ t < End`。
- `RenewTill == 0` 表示不可续期。
- `Invalid == true` 表示后置票据尚未通过 `Validate`。
- `Issued` 记录签发动作发生时的 `now`（后置票据记签发时刻而非 `Start`）；
  `Renew`、`Validate` 均不刷新 `Issued`。

## 3. 各操作与时间推导公式

### 3.1 `IssueTGT(subject, start, till, renewTill, now)`

1. 有效起始：`s = (start == 0) ? now : start`；非零 `start` 不得小于 `now`。
2. 区间：`till > s`，否则 `bad_interval`。
3. 后置：`s ≤ now + P`，否则 `postdated_too_far`。
4. 结束时刻：

   ```
   end = min(till, s + L)
   ```

5. 可续期上限：

   ```
   rt  = (renewTill > 0) ? min(renewTill, s + R) : 0
   RenewTill = (rt > end) ? rt : 0        // 恰等于 end 也视为不可续期
   ```

6. `s > now` 的后置票据 `Invalid = true`；`Issued = now`。

### 3.2 `TGS(tgtID, service, till, renewTill, now)`

先按固定次序检查 TGT（见 §4），通过后：

```
start = now
end   = min(till, now + L, TGT.End)
rt    = (renewTill > 0 且 TGT 可续期)
          ? min(renewTill, now + R, TGT.RenewTill) : 0
RenewTill = (rt > end) ? rt : 0
```

服务票据 `Issued = now`，主体与 TGT 相同。其 `End`/`RenewTill` 在签发瞬间
被裁剪固定，此后 TGT 再续期也不会延长它。

### 3.3 `Renew(id, now)`

先通过已生效 / 无效标志 / 未过期 / 密钥检查，再要求 `RenewTill > 0`
（否则 `not_renewable`）。记当前寿命 `life = End − Start`：

```
e' = min(now + life, RenewTill)
要求 e' > End，否则 renew_limit
成功则 Start = now, End = e'，RenewTill 不变，Issued 不变
```

注意寿命取 **当前** 的 `End − Start`：临近 `RenewTill` 时寿命会缩短。

### 3.4 `Validate(id, now)`

仅后置票据可验证：`Invalid` 必须为真（否则 `not_postdated`），随后要求
`Start ≤ now < End`，再做密钥检查，成功后 `Invalid = false`，其它字段不变。

### 3.5 `Authenticate(id, authTime, now)`（服务端校验）

次序固定：`clock_skew` → 已生效 → 无效标志 → 未过期 → 密钥 → 重放。

- `|now − authTime| > S` 报 `clock_skew`（最先检查，先于票据存在与时间检查）。
- 缓存键为 `(票据编号, authTime)`；命中且仍在保留窗口内报 `replay`。
- 只有成功的校验写入缓存；任何拒绝都不写入。

### 3.6 `ChangeKey(subject, now)`

记录该主体当前改密时刻 `c = now`（同一主体每次取新值；未出现过的主体也
接受）。凡 `Issued < c` 的该主体票据一律视为失效，`Issued == c` 不失效。
TGS、Renew、Validate、Authenticate 均在「已过期」之后追加「密钥已更换」
检查；`IssueTGT` 不受改密影响。

## 4. 固定判定次序（只报第一个失败原因）

所有操作的共同前缀：

1. `invalid_param`：字段越界（时间不在 `[0,10^15]`）、主体/服务名为空、
   服务名等于 `krbtgt`、非零 `start < now`、票据编号非正、`now` 越界等。
2. `clock_rewind`：`now < 已接受操作的最大 now`。
3. 票据存在性：找不到为 `ticket_not_found`；TGS 编号不是 TGT 为 `not_a_tgt`。

之后各操作的专属次序：

- IssueTGT：`bad_interval`（`till ≤ s`）→ `postdated_too_far`。
- TGS：尚未生效 `not_yet_valid` → 无效标志 `invalid_flag` → 已过期
  `expired` → 密钥已更换 `key_changed` → `bad_interval`（`till ≤ now`）。
- Renew：尚未生效 → 无效标志 → 已过期 → 密钥已更换 → 不可续期
  `not_renewable` → 续期上限 `renew_limit`。
- Validate：非后置 `not_postdated` → 尚未生效 → 已过期 → 密钥已更换。
- Authenticate：`clock_skew`（最前）→ 尚未生效 → 无效标志 → 已过期 →
  密钥已更换 → 重放 `replay`。

被拒绝的操作不改变任何票据、重放缓存、编号计数与时钟。

## 5. 重放缓存的保留范围与摊还回收

- 条目 `(id, authTime)` 的保留上界为 `authTime + S`：当
  `now > authTime + S` 时视同不存在（恰等号时 **仍** 保留、仍算重放；
  此时同一 `authTime` 再认证必先触发 `clock_skew`）。
- 缓存大小只与最近 `S` 秒窗口内的成功认证数有关，与历史总数无关。
- 实现为「按 `authTime + S` 排序的最小堆 + map 去重」。每次被接受的操作
  在时钟检查通过后执行一次回收：从堆顶弹出所有 `expire ≤ now` 的条目并同步
  删除 map 键，遇到第一个未到期条目即停。
- 非导出计数器 `popCount` 记录累计堆弹出次数（含每次回收至多一次的
  「探顶停止」），因此每次接受操作的弹出次数
  `≤ 本次到期条目数 + 1`，查询/登入摊还 O(1)。

## 6. 并发

`KDC` 内部以单一互斥锁串行化所有状态变更，因此并发调用的结果等价于某个
合法的串行顺序；同一 `(票据, authTime)` 并发认证恰有一个成功，其余报
`replay`。相同操作序列重放得到完全相同的编号、起止时刻与错误。

## 7. 本地验证

```bash
go test ./...                       # 全部用例（含 2000 组随机差分）
go test -race -count=3 ./...       # 并发与竞态
go test -v -run TestDifferentialAgainstNaive ./kerberos
                                    # 打印每组序列的输入、输出与判定依据
go test -cover ./...
gofmt -l . && go vet ./...
```

测试构成：

- `kdc_test.go` / `keys_test.go`：边界与固定次序用例（`end` 取较小者、
  `rt == end` 与 `rt == end+1`、`start == now+P` 边界、后置票据在
  `now == start` 时仍需 Validate、服务票据受 TGT 裁剪且不随后续续期延长、
  服务票据续期独立于 TGT、当前寿命续期与 `e' == end`、`now == end` 过期、
  偏差等于 `S` 与 `S+1`、偏差先于票据检查、缓存边界 `authTime+S`、
  被拒认证不入缓存、`issued` 与 `c` 的边界、续期不刷新 `issued`、
  后置/服务票据按签发时刻与主体失效、改密推进时钟、被拒不改状态等）。
- `diff_test.go` + `diff_run_test.go`：朴素模拟器（线性扫描缓存、逐条直写
  规则）与实现对照 2000 组随机 IssueTGT/TGS/Renew/Validate/Authenticate/
  ChangeKey 序列；每步比对拒绝原因、票据全字段、编号、时钟、缓存集合，并
  校验 `popCount` 的摊还弹出上界；`-v` 时打印输入、输出与判定依据。
- `concurrent_test.go`：同一 `(票据, authTime)` 并发认证恰一次成功，以及
  高并发下的串行等价性。
