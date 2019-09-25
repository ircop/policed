package policied

import (
	"errors"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/time/rate"
)

var ErrConnClosed = errors.New("connection is closed")

type WrappedConn struct {
	conn        net.Conn
	bps         uint64
	maxChunk    uint64
	chunkSize   uint64
	BurstFactor *BurstFactor

	limiter *rate.Limiter

	sizes      chan<- uint64
	permits    <-chan struct{}
	closed     bool
	onceCloser sync.Once
}

//func WrapConn(conn net.Conn, bps uint64, check chan<- uint32, release <-chan struct{}) net.Conn {
func WrapConn(conn net.Conn, bps uint64, maxChunk uint64, burstFactor *BurstFactor,
	sizes chan<- uint64, permits <-chan struct{}) *WrappedConn {
	wc := WrappedConn{
		conn:        conn,
		bps:         bps, // bytes, not bits
		sizes:       sizes,
		permits:     permits,
		limiter:     rate.NewLimiter(rate.Inf, 0),
		BurstFactor: burstFactor,
	}

	wc.setRate(bps, maxChunk)

	return &wc
}

// internal, called from policed
func (c *WrappedConn) setRate(bps uint64, maxChunk uint64) {
	atomic.StoreUint64(&c.bps, bps)
	c.calcChunk(maxChunk)

	limit := rate.Limit(bps)
	if bps == 0 {
		limit = rate.Inf
	}

	c.setLimiter(rate.NewLimiter(limit, int(c.chunkSize)))
}

// SetRate allows to set individual rate per each connection - public interface
func (c *WrappedConn) SetRate(kbps uint64) {
	bps := kbps * 1024
	atomic.StoreUint64(&c.bps, bps)
	limit := rate.Limit(bps)
	if bps == 0 {
		limit = rate.Inf
	}

	c.setLimiter(rate.NewLimiter(limit, int(bps)))
}

// Write to original connection, limiting with both conn/global rate limits
func (c *WrappedConn) Write(b []byte) (int, error) {
	limiter := c.getCurrentLimiter()

	chunkSize := atomic.LoadUint64(&c.chunkSize)
	bps := atomic.LoadUint64(&c.bps)
	if chunkSize == 0 && bps == 0 {
		return c.conn.Write(b)
	}

	// i wish go had cpp iterators...
	var wrote int
	var i uint64
	bytes := uint64(len(b))

	for i = 0; i < bytes; i += chunkSize {
		now := time.Now()
		// first check global limits, then local
		if i+chunkSize > bytes {
			// send whole chunkSize
			toWrite := chunkSize - (i + chunkSize - bytes)

			c.sizes <- toWrite
			<-c.permits

			reservation := limiter.ReserveN(now, int(toWrite))
			time.Sleep(reservation.DelayFrom(now))

			n, err := c.conn.Write(b[i:])
			wrote += n
			return wrote, err
		}

		// send partial chunkSize
		c.sizes <- chunkSize
		<-c.permits

		reservation := limiter.ReserveN(now, int(chunkSize))
		time.Sleep(reservation.DelayFrom(now))

		n, err := c.conn.Write(b[i : i+chunkSize])
		wrote += n
		if err != nil {
			return wrote, err
		}
	}

	return wrote, nil
}

// Calculate chunk
func (c *WrappedConn) calcChunk(max uint64) {
	bps := atomic.LoadUint64(&c.bps)
	atomic.StoreUint64(&c.maxChunk, max)

	var chunkSize uint64
	if bps == 0 {
		chunkSize = max
	} else {
		chunkSize = uint64(math.Ceil(float64(bps) * c.BurstFactor.Get()))
	}

	atomic.StoreUint64(&c.chunkSize, chunkSize)
	c.setLimiter(rate.NewLimiter(rate.Limit(bps), int(chunkSize)))
}

func (c *WrappedConn) Read(b []byte) (n int, err error) {
	return c.conn.Read(b)
}
func (c *WrappedConn) Close() error {
	if c.closed {
		return ErrConnClosed
	}
	c.onceCloser.Do(func() {
		close(c.sizes)
		c.closed = true
	})
	return c.conn.Close()
}
func (c *WrappedConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}
func (c *WrappedConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}
func (c *WrappedConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}
func (c *WrappedConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}
func (c *WrappedConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// take pointer to current limiter
func (c *WrappedConn) getCurrentLimiter() *rate.Limiter {
	return (*rate.Limiter)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&c.limiter))))
}

// replace current limiter with new one
func (c *WrappedConn) setLimiter(limiter *rate.Limiter) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&c.limiter)), unsafe.Pointer(limiter))
}
