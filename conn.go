package policied

import (
	"errors"
	"golang.org/x/time/rate"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var ErrConnClosed = errors.New("Connection is closed")

type WrappedConn struct {
	conn		net.Conn
	bps			uint64
	chunkSize	uint64

	limiter		*rate.Limiter
	limiterLock	sync.Mutex

	sizes		chan<- uint64
	permits		<-chan struct{}
	closed		bool
	onceCloser	sync.Once
}

//func WrapConn(conn net.Conn, bps uint64, check chan<- uint32, release <-chan struct{}) net.Conn {
func WrapConn(conn net.Conn, bps uint64, maxChunk uint64, sizes chan<- uint64, permits <-chan struct{}) *WrappedConn {
	wc := WrappedConn{
		conn:conn,
		bps:bps,		// bytes, not bits
		sizes:sizes,
		permits:permits,
		limiter:rate.NewLimiter(rate.Inf, 0),
	}

	wc.SetLimit(bps)
	wc.CalcChunk(maxChunk)

	return &wc
}

func (c *WrappedConn) SetLimit(bps uint64) {
	atomic.StoreUint64(&c.bps, bps)
	if bps == 0 {
		return
	}

	c.limiterLock.Lock()
	c.limiter = rate.NewLimiter(rate.Limit(bps), int(bps))
	c.limiterLock.Unlock()
}

func (c WrappedConn) Write(b []byte) (int, error) {
	if c.bps == 0 {
		return c.conn.Write(b)
	}

	// copy limiter pointer so that we can thread-safely replace limiter
	c.limiterLock.Lock()
	limiter := (*rate.Limiter)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&c.limiter))))
	c.limiterLock.Unlock()

	chunkSize := atomic.LoadUint64(&c.chunkSize)
	bps := atomic.LoadUint64(&c.bps)
	if chunkSize > bps {
		chunkSize = bps
	}

	// i wish go had cpp iterators...
	var wrote int
	var i uint64
	bytes := uint64(len(b))
	for i = 0; i < bytes; i += chunkSize {
		now := time.Now()
		// first check global limits, then local
		if i +chunkSize > bytes {
			// send whole chunkSize
			toWrite := chunkSize - (i+ chunkSize - bytes)

			c.sizes <- toWrite
			<-c.permits

			reservation := limiter.ReserveN(now, int(toWrite))
			time.Sleep(reservation.DelayFrom(now))

			n, err := c.conn.Write(b[i:])
			wrote += n
			return wrote, err
		} else {
			// send partial chunkSize
			c.sizes <- chunkSize
			<-c.permits

			reservation := limiter.ReserveN(now, int(chunkSize))
			time.Sleep(reservation.DelayFrom(now))

			n, err := c.conn.Write(b[i:i+chunkSize])
			wrote += n
			if err != nil {
				return wrote, err
			}
		}
	}

	return wrote, nil
}

func (c *WrappedConn) CalcChunk(max uint64) {
	if max > c.bps {
		atomic.StoreUint64(&c.chunkSize, atomic.LoadUint64(&c.bps))
	} else {
		atomic.StoreUint64(&c.chunkSize, max)
	}
}

func (c WrappedConn) Read(b []byte) (n int, err error) {
	return c.conn.Read(b)
}
func (c WrappedConn) Close() error {
	if c.closed {
		return ErrConnClosed
	}
	c.onceCloser.Do(func() {
		close(c.sizes)
		c.closed = true
	})
	return c.conn.Close()
}
func (c WrappedConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}
func (c WrappedConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}
func (c WrappedConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}
func (c WrappedConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}
func (c WrappedConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}
