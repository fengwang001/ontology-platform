package van

type invalidErr string

func (e invalidErr) Error() string { return string(e) }

type dupErr string

func (e dupErr) Error() string { return string(e) }

type stopErr string

func (e stopErr) Error() string { return string(e) }

type orderErr string

func (e orderErr) Error() string { return string(e) }

type notFoundErr string

func (e notFoundErr) Error() string { return string(e) }

type processedErr string

func (e processedErr) Error() string { return string(e) }
