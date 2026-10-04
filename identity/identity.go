package identity

import "errors"

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockSkew       = errors.New("clock skew")
	ErrAlreadyBound    = errors.New("already bound")
	ErrNotBound        = errors.New("not bound")
)

type Binder struct {
	maxNow  int64
	devices map[string]string
}

func New() *Binder {
	return &Binder{devices: map[string]string{}}
}

func (b *Binder) Advance(now int64) error {
	if err := b.Check(now); err != nil {
		return err
	}
	b.maxNow = now
	return nil
}

func (b *Binder) Check(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if now < b.maxNow {
		return ErrClockSkew
	}
	return nil
}

func (b *Binder) Login(now int64, device, user string) (string, error) {
	if device == "" || user == "" {
		return "", ErrInvalidArgument
	}
	if err := b.Check(now); err != nil {
		return "", err
	}
	if _, bound := b.devices[device]; bound {
		return "", ErrAlreadyBound
	}
	b.maxNow = now
	b.devices[device] = user
	return user, nil
}

func (b *Binder) Logout(now int64, device string) error {
	if device == "" {
		return ErrInvalidArgument
	}
	if err := b.Check(now); err != nil {
		return err
	}
	if _, bound := b.devices[device]; !bound {
		return ErrNotBound
	}
	b.maxNow = now
	delete(b.devices, device)
	return nil
}

func (b *Binder) Subject(device string) string {
	if user, bound := b.devices[device]; bound {
		return user
	}
	return device
}

func (b *Binder) BoundUser(device string) (string, bool) {
	user, bound := b.devices[device]
	return user, bound
}
