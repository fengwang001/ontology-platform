package slotting

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// LoggedService 为 Service 的日志装饰器：逐操作打印输入、输出与判定依据。
// 它与被装饰对象共享同一 Store，因此并发语义与原子性完全一致。
type LoggedService struct {
	inner *Service
	w     io.Writer
}

// NewLoggedService 包装服务并输出文本日志。
func NewLoggedService(inner *Service, w io.Writer) *LoggedService {
	return &LoggedService{inner: inner, w: w}
}

func (l *LoggedService) logf(format string, args ...any) {
	fmt.Fprintln(l.w, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func palletString(p Pallet) string {
	return fmt.Sprintf(
		"{id:%s product:%s batch:%s category:%d weight:%d height:%d}",
		p.ID, p.Product, p.Batch, p.Category, p.Weight, p.Height)
}

func coordString(c Coord) string {
	return fmt.Sprintf("(A%d,L%d,P%d)", c.Aisle, c.Level, c.Position)
}

func resultString(c Coord, err error) string {
	if err != nil {
		return "REJECT reason=" + string(AsReason(err)) + " :: " + err.Error()
	}
	return "OK -> " + coordString(c)
}

// AutoPutaway 记录自动上架的输入、候选选择结果或拒绝原因。
func (l *LoggedService) AutoPutaway(p Pallet) (Coord, error) {
	c, err := l.inner.AutoPutaway(p)
	l.logf("AutoPutaway in=%s out=%s", palletString(p), resultString(c, err))
	return c, err
}

// PutawayTo 记录指定货位上架。
func (l *LoggedService) PutawayTo(p Pallet, target Coord) error {
	err := l.inner.PutawayTo(p, target)
	l.logf("PutawayTo in=%s target=%s out=%s",
		palletString(p), coordString(target), resultString(Coord{}, err))
	return err
}

// Move 记录移库。
func (l *LoggedService) Move(palletID string, target Coord) error {
	err := l.inner.Move(palletID, target)
	l.logf("Move in=%s target=%s out=%s",
		palletID, coordString(target), resultString(Coord{}, err))
	return err
}

// Retrieve 记录取出。
func (l *LoggedService) Retrieve(palletID string) error {
	err := l.inner.Retrieve(palletID)
	l.logf("Retrieve in=%s out=%s", palletID, resultString(Coord{}, err))
	return err
}

// Freeze / Unfreeze 记录冻结状态切换。
func (l *LoggedService) Freeze(c Coord) error {
	err := l.inner.Freeze(c)
	l.logf("Freeze in=%s out=%s", coordString(c), resultString(Coord{}, err))
	return err
}

func (l *LoggedService) Unfreeze(c Coord) error {
	err := l.inner.Unfreeze(c)
	l.logf("Unfreeze in=%s out=%s", coordString(c), resultString(Coord{}, err))
	return err
}

// BatchPutaway 记录批量上架结果及最小失败下标。
func (l *LoggedService) BatchPutaway(pallets []Pallet) (map[string]Coord, error) {
	res, err := l.inner.BatchPutaway(pallets)
	if err != nil {
		var idx int
		var reason Reason
		var be *BatchError
		if errors.As(err, &be) {
			idx = be.Index
			reason = AsReason(be.Err)
		}
		l.logf("BatchPutaway n=%d REJECT index=%d reason=%s", len(pallets), idx, reason)
		return res, err
	}
	parts := make([]string, 0, len(res))
	for id, c := range res {
		parts = append(parts, id+"->"+coordString(c))
	}
	l.logf("BatchPutaway n=%d OK %s", len(pallets), strings.Join(parts, " "))
	return res, err
}

// Location / PalletLocation / ProductLocations 记录只读查询。
func (l *LoggedService) Location(c Coord) (LocationView, error) {
	v, err := l.inner.Location(c)
	if err != nil {
		l.logf("Location in=%s REJECT %s", coordString(c), err.Error())
		return v, err
	}
	l.logf("Location in=%s occupied=%d/%d remainingWeight=%d pallets=%v",
		coordString(c), v.Occupied, v.Capacity, v.RemainingWeight, v.PalletIDs)
	return v, err
}

func (l *LoggedService) PalletLocation(palletID string) (Coord, error) {
	c, err := l.inner.PalletLocation(palletID)
	l.logf("PalletLocation in=%s out=%s", palletID, resultString(c, err))
	return c, err
}

func (l *LoggedService) ProductLocations(product string) []Coord {
	out := l.inner.ProductLocations(product)
	parts := make([]string, len(out))
	for i, c := range out {
		parts[i] = coordString(c)
	}
	l.logf("ProductLocations in=%s out=%s", product, strings.Join(parts, ","))
	return out
}
