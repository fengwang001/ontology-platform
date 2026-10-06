package cookie

import (
	"container/heap"
	"sort"
	"strings"
)

// acceptClock 在校验通过后统一推进时钟。
func (k *Kernel) acceptClock(now int64) {
	k.now = now
	k.clockSet = true
}

// pathMatch 按 RFC6265 的路径边界规则判定：整段匹配而非任意字符前缀。
func pathMatch(cookiePath, targetPath string) bool {
	if !strings.HasPrefix(targetPath, cookiePath) {
		return false
	}
	if cookiePath == "/" || len(cookiePath) == len(targetPath) {
		return true
	}
	// cookie 路径本身以斜杠结尾时，前缀命中即整段命中；
	// 否则要求目标路径的下一个字符是斜杠（避免 /foo 命中 /foobar）。
	if cookiePath[len(cookiePath)-1] == '/' {
		return true
	}
	return targetPath[len(cookiePath)] == '/'
}

// sameSiteAllowed 叠加同站规则；mode 为空串表示未声明（宽松+跨站宽限）。
func sameSiteAllowed(mode SameSite, req RequestInput, createdAt, now int64, grace int64) (bool, string) {
	sameSite := req.Initiator == req.TargetDomain && req.Initiator != ""
	switch mode {
	case SameSiteStrict:
		if sameSite {
			return true, "strict: same-site"
		}
		return false, "strict: cross-site rejected"
	case SameSiteLax:
		if sameSite {
			return true, "lax: same-site"
		}
		if req.TopNav && req.SafeMethod {
			return true, "lax: top-level safe-method navigation"
		}
		return false, "lax: cross-site non-safe/non-top-level rejected"
	case SameSiteNone:
		return true, "none: no same-site constraint"
	case "":
		// 未显式声明：宽松语义 + 创建后 grace 内允许跨站非安全方法的顶层导航。
		if sameSite {
			return true, "default-lax: same-site"
		}
		if req.TopNav && req.SafeMethod {
			return true, "default-lax: top-level safe-method navigation"
		}
		if req.TopNav && !req.SafeMethod && now-createdAt < grace {
			return true, "default-lax: within cross-site grace window"
		}
		return false, "default-lax: cross-site rejected"
	default:
		return false, "unknown same-site mode"
	}
}

// evaluate 对单个候选条目做完整判定，返回是否附带与首个失败依据。
func (k *Kernel) evaluate(e *entry, req RequestInput) (bool, string) {
	if !pathMatch(e.key.Path, req.TargetPath) {
		return false, "path boundary mismatch"
	}
	if e.secure && !req.Secure {
		return false, "secure cookie over non-secure request"
	}
	if e.expires != nil && *e.expires <= req.Now {
		return false, "expired"
	}
	if e.key.Partition != req.Partition {
		return false, "partition mismatch"
	}
	return sameSiteAllowed(e.sameSite, req, e.createdAt, req.Now, k.cfg.LaxGrace)
}

func validateRequest(req RequestInput) error {
	if req.TargetDomain == "" {
		return ErrEmptyDomain
	}
	if req.TargetPath == "" || req.TargetPath[0] != '/' {
		return ErrBadPath
	}
	return nil
}

// Attach 执行一次请求附带判定：只触及目标站点的路径前缀桶，
// 附带成功的条目更新最近访问时刻，并输出逐条判定依据。
func (k *Kernel) Attach(req RequestInput) (RequestResult, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if err := validateRequest(req); err != nil {
		k.log.Printf("ATTACH REJECTED req=%+v reason=%v", req, err)
		return RequestResult{}, err
	}
	if err := k.checkClock(req.Now); err != nil {
		k.log.Printf("ATTACH REJECTED req=%+v reason=%v", req, err)
		return RequestResult{}, err
	}
	k.acceptClock(req.Now)

	result := RequestResult{
		Sent:        []Entry{},
		Evaluations: []Evaluation{},
	}
	s, ok := k.sites[req.TargetDomain]
	if !ok {
		k.pathProbes = 0
		k.log.Printf("ATTACH OK req=%+v sent=0 probes=0", req)
		return result, nil
	}

	// 收集目标路径各整段前缀桶内的候选；去重并惰性移除过期者。
	candidates := make(map[Key]*entry)
	probes := 0
	s.paths.walkAncestors(req.TargetPath, func(bucket map[Key]*entry) {
		for key, e := range bucket {
			probes++
			if e.expires != nil && *e.expires <= req.Now {
				// 过期条目在被观察的读取路径上立即移除并计入过期淘汰。
				delete(candidates, key)
				k.removeFromExpiryHeaps(s, e)
				k.removeEntryNoExpiry(s, key)
				k.expiryEvicted++
				result.Evaluations = append(result.Evaluations, Evaluation{
					Entry: e.snapshot(), Send: false, Reason: "expired (evicted on observation)",
				})
				continue
			}
			candidates[key] = e
		}
	})
	k.pathProbes = probes

	ordered := make([]*entry, 0, len(candidates))
	for _, e := range candidates {
		ordered = append(ordered, e)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if len(ordered[i].key.Path) != len(ordered[j].key.Path) {
			return len(ordered[i].key.Path) > len(ordered[j].key.Path)
		}
		if ordered[i].createdAt != ordered[j].createdAt {
			return ordered[i].createdAt < ordered[j].createdAt
		}
		return keyString(ordered[i].key) < keyString(ordered[j].key)
	})

	for _, e := range ordered {
		send, reason := k.evaluate(e, req)
		result.Evaluations = append(result.Evaluations, Evaluation{
			Entry: e.snapshot(), Send: send, Reason: reason,
		})
		if !send {
			continue
		}
		e.lastAccess = req.Now
		heap.Fix(&s.lru, e.lruIndex)
		result.Sent = append(result.Sent, e.snapshot())
	}

	k.log.Printf("ATTACH OK req=%+v probes=%d sent=%d evals=%d",
		req, probes, len(result.Sent), len(result.Evaluations))
	for _, ev := range result.Evaluations {
		k.log.Printf("  eval key=%+v send=%v reason=%s",
			ev.Entry.Name, ev.Send, ev.Reason)
	}
	return result, nil
}

// ReadScript 模拟脚本读取：仅 HTTP 条目不可见；过期者被观察即移除。
func (k *Kernel) ReadScript(domain, targetPath, partition string, now int64) ([]Entry, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if domain == "" {
		err := ErrEmptyDomain
		k.log.Printf("READ REJECTED domain=%q reason=%v", domain, err)
		return nil, err
	}
	if targetPath == "" || targetPath[0] != '/' {
		err := ErrBadPath
		k.log.Printf("READ REJECTED path=%q reason=%v", targetPath, err)
		return nil, err
	}
	if err := k.checkClock(now); err != nil {
		k.log.Printf("READ REJECTED now=%d reason=%v", now, err)
		return nil, err
	}
	k.acceptClock(now)

	out := []Entry{}
	s, ok := k.sites[domain]
	if !ok {
		k.log.Printf("READ OK domain=%s path=%s found=0", domain, targetPath)
		return out, nil
	}

	seen := make(map[Key]bool)
	s.paths.walkAncestors(targetPath, func(bucket map[Key]*entry) {
		for key, e := range bucket {
			if seen[key] {
				continue
			}
			seen[key] = true
			if e.expires != nil && *e.expires <= now {
				k.removeFromExpiryHeaps(s, e)
				k.removeEntryNoExpiry(s, key)
				k.expiryEvicted++
				continue
			}
			if e.httpOnly || e.key.Partition != partition || !pathMatch(e.key.Path, targetPath) {
				continue
			}
			out = append(out, e.snapshot())
		}
	})
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Path) != len(out[j].Path) {
			return len(out[i].Path) > len(out[j].Path)
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return out[i].Name < out[j].Name
	})
	k.log.Printf("READ OK domain=%s path=%s partition=%q found=%d", domain, targetPath, partition, len(out))
	return out, nil
}
