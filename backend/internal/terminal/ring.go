package terminal

type Ring struct {
	data  []byte
	limit int
}

func NewRing(limit int) *Ring { return &Ring{limit: limit} }

func (r *Ring) Append(chunk []byte) {
	if r.limit <= 0 || len(chunk) == 0 {
		return
	}
	if len(chunk) >= r.limit {
		r.data = append(r.data[:0], chunk[len(chunk)-r.limit:]...)
		return
	}
	overflow := len(r.data) + len(chunk) - r.limit
	if overflow > 0 {
		copy(r.data, r.data[overflow:])
		r.data = r.data[:len(r.data)-overflow]
	}
	r.data = append(r.data, chunk...)
}

func (r *Ring) Bytes() []byte { return append([]byte(nil), r.data...) }
