package stream

type InvalidError struct {
	Offset int
	Len    int
}

func (e *InvalidError) Error() string { return ErrInvalid.Error() }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

type TruncatedError struct {
	Offset int
	Len    int
}

func (e *TruncatedError) Error() string { return ErrTruncated.Error() }
func (e *TruncatedError) Unwrap() error { return ErrTruncated }
