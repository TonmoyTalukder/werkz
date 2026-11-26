package browser

// ring is a fixed-capacity buffer that keeps the newest items. Adding is O(1)
// and never copies the buffer.
type ring[T any] struct {
	buf   []T
	start int // index of the oldest item
	n     int
}

func newRing[T any](capacity int) *ring[T] { return &ring[T]{buf: make([]T, capacity)} }

// add appends v and returns the item it pushed out, if any.
func (r *ring[T]) add(v T) (evicted T, did bool) {
	if r.n == len(r.buf) {
		evicted, did = r.buf[r.start], true
		r.buf[r.start] = v
		r.start = (r.start + 1) % len(r.buf)
		return evicted, did
	}
	r.buf[(r.start+r.n)%len(r.buf)] = v
	r.n++
	return evicted, false
}

// items returns the contents, oldest first, in a fresh slice.
func (r *ring[T]) items() []T {
	out := make([]T, 0, r.n)
	for i := 0; i < r.n; i++ {
		out = append(out, r.buf[(r.start+i)%len(r.buf)])
	}
	return out
}

func (r *ring[T]) clear() {
	var zero T
	for i := range r.buf {
		r.buf[i] = zero
	}
	r.start, r.n = 0, 0
}
