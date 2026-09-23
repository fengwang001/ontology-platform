package stream

type DecodeError struct {
	Offset int
	Err    error
}

func (e *DecodeError) Error() string {
	if e == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *DecodeError) Unwrap() error {
	return e.Err
}

type Decoder struct {
	mime  bool
	limit int
	bytesChecked int

	err error
}

func NewDecoder(mime bool, limit int) *Decoder {
	_ = mime
	_ = limit
	return &Decoder{}
}

func (d *Decoder) Write(p []byte) (int, error) {
	_ = p
	return 0, nil
}

func (d *Decoder) Close() error { return nil }

func (d *Decoder) Output() []byte { return nil }

func (d *Decoder) BytesChecked() int { return d.bytesChecked }

type Encoder struct {
	mime bool
}

func NewEncoder(mime bool) *Encoder {
	_ = mime
	return &Encoder{}
}

func (e *Encoder) Write(p []byte) (int, error) {
	_ = p
	return 0, nil
}

func (e *Encoder) Close() error { return nil }

func (e *Encoder) Output() []byte { return nil }
