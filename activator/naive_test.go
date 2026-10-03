package activator

import (
	"errors"
	"sort"

	"ontology/config"
)

// 朴素模拟器：严格按规格逐步写成，出错即整体回滚；与产品实现刻意独立。
type nCluster struct{ serving, warming, since int64 }
type nRoute struct {
	sVer, pVer int64
	sRef, pRef []string
}

type naive struct {
	w               int64
	lastVer, maxNow int64
	clusters        map[string]*nCluster
	routes          map[string]*nRoute
}

type nResult struct {
	errCode string
	val     int64
	ok      bool
}

func newNaive(w int64) *naive {
	return &naive{w: w, clusters: map[string]*nCluster{}, routes: map[string]*nRoute{}}
}

func (n *naive) clone() *naive {
	c := &naive{w: n.w, lastVer: n.lastVer, maxNow: n.maxNow,
		clusters: map[string]*nCluster{}, routes: map[string]*nRoute{}}
	for k, v := range n.clusters {
		cv := *v
		c.clusters[k] = &cv
	}
	for k, v := range n.routes {
		cv := *v
		cv.sRef = append([]string(nil), v.sRef...)
		cv.pRef = append([]string(nil), v.pRef...)
		c.routes[k] = &cv
	}
	return c
}

func (n *naive) refsReady(refs []string) bool {
	for _, r := range refs {
		c, ok := n.clusters[r]
		if !ok || c.serving == 0 || c.warming != 0 {
			return false
		}
	}
	return true
}

func (n *naive) cascade() {
	for {
		names := make([]string, 0)
		for k, r := range n.routes {
			if r.pVer != 0 {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		moved := false
		for _, k := range names {
			r := n.routes[k]
			if r.pVer != 0 && n.refsReady(r.pRef) {
				r.sVer = r.pVer
				r.sRef = append([]string(nil), r.pRef...)
				r.pVer, r.pRef = 0, nil
				moved = true
			}
		}
		if !moved {
			return
		}
	}
}

// settleOne 按 (since,name) 升序处理一个超时集群。
func (n *naive) settleOne(now int64) bool {
	type key struct {
		s    int64
		name string
	}
	var keys []key
	for name, c := range n.clusters {
		if c.warming != 0 && c.since+n.w <= now {
			keys = append(keys, key{c.since, name})
		}
	}
	if len(keys) == 0 {
		return false
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].s != keys[j].s {
			return keys[i].s < keys[j].s
		}
		return keys[i].name < keys[j].name
	})
	name := keys[0].name
	c := n.clusters[name]
	if c.serving != 0 {
		c.warming, c.since = 0, 0
		n.cascade()
	} else {
		delete(n.clusters, name)
		for rn, r := range n.routes {
			if r.pVer == 0 {
				continue
			}
			for _, ref := range r.pRef {
				if ref == name {
					r.pVer, r.pRef = 0, nil
					if r.sVer == 0 {
						delete(n.routes, rn)
					}
					break
				}
			}
		}
	}
	return true
}

func (n *naive) settle(now int64) {
	for n.settleOne(now) {
	}
}

func naiveValidate(in config.PushInput) string {
	seenC := map[string]bool{}
	for _, c := range in.Clusters {
		if c.Name == "" || seenC[c.Name] {
			return "arg"
		}
		seenC[c.Name] = true
	}
	seenR := map[string]bool{}
	for _, r := range in.Routes {
		if r.Name == "" || seenR[r.Name] || len(r.Clusters) < 1 || len(r.Clusters) > 8 {
			return "arg"
		}
		seenR[r.Name] = true
		dup := map[string]bool{}
		for _, x := range r.Clusters {
			if x == "" || dup[x] {
				return "arg"
			}
			dup[x] = true
		}
	}
	dc := map[string]bool{}
	for _, x := range in.DeleteClusters {
		if x == "" || dc[x] || seenC[x] {
			return "arg"
		}
		dc[x] = true
	}
	dr := map[string]bool{}
	for _, x := range in.DeleteRoutes {
		if x == "" || dr[x] || seenR[x] {
			return "arg"
		}
		dr[x] = true
	}
	return ""
}

func (n *naive) push(ver int64, in config.PushInput, now int64) nResult {
	if code := naiveValidate(in); code != "" {
		return nResult{errCode: code}
	}
	if now < 0 || now > config.MaxTime {
		return nResult{errCode: "time"}
	}
	if now < n.maxNow {
		return nResult{errCode: "back"}
	}
	d := n.clone()
	d.settle(now)
	if ver != d.lastVer+1 {
		return nResult{errCode: "ver"}
	}
	postC := map[string]bool{}
	for k := range d.clusters {
		postC[k] = true
	}
	for _, k := range in.DeleteClusters {
		delete(postC, k)
	}
	for _, c := range in.Clusters {
		postC[c.Name] = true
	}
	for _, r := range in.Routes {
		for _, x := range r.Clusters {
			if !postC[x] {
				return nResult{errCode: "dangle"}
			}
		}
	}
	type view struct{ sRef, pRef []string }
	postR := map[string]view{}
	for k, r := range d.routes {
		postR[k] = view{r.sRef, r.pRef}
	}
	for _, k := range in.DeleteRoutes {
		delete(postR, k)
	}
	for _, r := range in.Routes {
		v := postR[r.Name]
		v.pRef = r.Clusters
		postR[r.Name] = v
	}
	for _, target := range in.DeleteClusters {
		for _, v := range postR {
			for _, x := range v.sRef {
				if x == target {
					return nResult{errCode: "inuse"}
				}
			}
			for _, x := range v.pRef {
				if x == target {
					return nResult{errCode: "inuse"}
				}
			}
		}
	}
	for _, k := range in.DeleteRoutes {
		delete(d.routes, k)
	}
	for _, c := range in.Clusters {
		x := d.clusters[c.Name]
		if x == nil {
			x = &nCluster{}
			d.clusters[c.Name] = x
		}
		x.warming, x.since = ver, now
	}
	for _, k := range in.DeleteClusters {
		delete(d.clusters, k)
	}
	for _, r := range in.Routes {
		x := d.routes[r.Name]
		if x == nil {
			x = &nRoute{}
			d.routes[r.Name] = x
		}
		x.pVer = ver
		x.pRef = append([]string{}, r.Clusters...)
	}
	d.lastVer = ver
	d.cascade()
	d.maxNow = now
	*n = *d
	return nResult{}
}

func (n *naive) ready(name string, now int64) nResult {
	if name == "" {
		return nResult{errCode: "arg"}
	}
	if now < 0 || now > config.MaxTime {
		return nResult{errCode: "time"}
	}
	if now < n.maxNow {
		return nResult{errCode: "back"}
	}
	d := n.clone()
	d.settle(now)
	c, ok := d.clusters[name]
	if !ok || c.warming == 0 {
		return nResult{errCode: "notwarm"}
	}
	c.serving, c.warming, c.since = c.warming, 0, 0
	var refs []string
	for rn, r := range d.routes {
		if r.pVer == 0 {
			continue
		}
		for _, x := range r.pRef {
			if x == name {
				refs = append(refs, rn)
				break
			}
		}
	}
	sort.Strings(refs)
	for _, rn := range refs {
		r := d.routes[rn]
		if d.refsReady(r.pRef) {
			r.sVer = r.pVer
			r.sRef = append([]string(nil), r.pRef...)
			r.pVer, r.pRef = 0, nil
		}
	}
	d.maxNow = now
	*n = *d
	return nResult{}
}

func (n *naive) query(kind, name string, now int64) nResult {
	if now < 0 || now > config.MaxTime {
		return nResult{errCode: "time"}
	}
	if now < n.maxNow {
		return nResult{errCode: "back"}
	}
	d := n.clone()
	d.settle(now)
	d.maxNow = now
	*n = *d
	if kind == "serving" {
		r, ok := n.routes[name]
		if !ok || r.sVer == 0 {
			return nResult{ok: false}
		}
		return nResult{val: r.sVer, ok: true}
	}
	c, ok := n.clusters[name]
	if !ok {
		return nResult{ok: false}
	}
	return nResult{val: c.serving, ok: true}
}

func codeOf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, config.ErrInvalidArgument):
		return "arg"
	case errors.Is(err, config.ErrInvalidTime):
		return "time"
	case errors.Is(err, config.ErrClockBackwards):
		return "back"
	case errors.Is(err, config.ErrBadVersion):
		return "ver"
	case errors.Is(err, config.ErrDangling):
		return "dangle"
	case errors.Is(err, config.ErrInUse):
		return "inuse"
	case errors.Is(err, config.ErrNotWarming):
		return "notwarm"
	default:
		return "?"
	}
}
