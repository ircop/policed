package policied

import (
	"golang.org/x/time/rate"
	"net"
	"time"
)

type WrappedConn struct {
	conn		net.Conn
	bps			uint64
	chunkSize	uint32

	check		chan<- uint32
	release		<-chan struct{}

	limiter		*rate.Limiter
}

//func WrapConn(conn net.Conn, bps uint64, check chan<- uint32, release <-chan struct{}) net.Conn {
func WrapConn(conn net.Conn, bps uint64) *WrappedConn {
	wc := WrappedConn{
		conn:conn,
		bps:bps,		// bytes, not bits
	}
	if bps > 0 {
		wc.limiter = rate.NewLimiter(rate.Limit(bps), int(bps))
	}
	return &wc
}


func (c WrappedConn) Write(b []byte) (int, error) {
	if c.bps == 0 {
		return c.conn.Write(b)
	}

	// todo: make smaller chunks
	// everything will be ok if global limit > current limit, but if it's less..
	chunk := int(c.bps)
	if len(b) > chunk {
		chunk = int(float64(chunk) / 30)		// we don't want to occupy whole server limit with single conn
	}

	// split bytes into chunks, with max size = bps
	// i wish go had cpp iterators...
	var wrote int
	for i := 0; i < len(b); i += chunk {
		now := time.Now()
		if i + chunk > len(b) {
			// send whole chunk
			toWrite := chunk - (i+chunk - len(b))
			reservation := c.limiter.ReserveN(now, toWrite)
			time.Sleep(reservation.DelayFrom(now))

			n, err := c.conn.Write(b[i:])
			wrote += n
			return wrote, err
		} else {
			// send partial chunk
			reservation := c.limiter.ReserveN(now, chunk)
			time.Sleep(reservation.DelayFrom(now))

			n, err := c.conn.Write(b[i:i+chunk])
			wrote += n
			if err != nil {
				return wrote, err
			}
		}
	}

	return wrote, nil
}

func (c WrappedConn) Read(b []byte) (n int, err error) {
	return c.conn.Read(b)
}
func (c WrappedConn) Close() error {
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
