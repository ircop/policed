package policied

import (
	"golang.org/x/time/rate"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type Policier struct {
	globalBps uint64
	connBPS   uint64
	connPool  sync.Map

	limiter  *rate.Limiter
	maxChunk uint64
}

// Creates new policier with given bandwidth limit in KBps (kiloBYtes)
// If bandwidth is zero, there is no limit
func NewPolicier(gloablKBPS uint64, connKBPS uint64) *Policier {
	policier := &Policier{
		connBPS: connKBPS * 1024,
		limiter: rate.NewLimiter(rate.Limit(0), 0),
	}
	policier.SetGlobalRate(gloablKBPS)

	return policier
}

// SetConnRate setc rate limit per each connection, including all currently existent
func (p *Policier) SetConnRate(kbps uint64) {
	connRate := kbps * 1024

	// check that conn.rate is <= global rate
	globalRate := atomic.LoadUint64(&p.connBPS)
	if globalRate > 0 && connRate > globalRate {
		connRate = globalRate
	}

	atomic.StoreUint64(&p.connBPS, connRate)

	p.limitConnections(kbps, atomic.LoadUint64(&p.maxChunk))
}

// SetGlobalRate sets global rate limit per server
func (p *Policier) SetGlobalRate(kbps uint64) {
	globalRate := kbps * 1024
	connRate := atomic.LoadUint64(&p.connBPS)

	// There is no sense to set global limit < conn limit
	if globalRate > 0 && globalRate < connRate {
		atomic.StoreUint64(&p.globalBps, connRate)
	} else {
		atomic.StoreUint64(&p.globalBps, globalRate)
	}

	// Calculate maximum chunk size.
	// We don't want single conn to occupy whole global limit
	var maxChunk uint64 = 128 * 1024
	if globalRate > 0 {
		maxChunk = uint64(math.Ceil(float64(globalRate) * 0.005))
	}
	atomic.StoreUint64(&p.maxChunk, maxChunk)

	p.setConnMaxChunk(maxChunk)

	limit := rate.Limit(globalRate)
	if globalRate == 0 {
		limit = rate.Inf
	}
	p.setLimiter(rate.NewLimiter(limit, int(maxChunk)))
}

// Wrap Accept() so that we can just replace
// `..... := listener.Accept` -> `...... := policier.Wrap(listener.Accept())`
func (p *Policier) Wrap(conn net.Conn, err error) (*WrappedConn, error) {
	if err != nil {
		return nil, err
	}

	// wrap and do stuff
	sizes := make(chan uint64)
	permits := make(chan struct{}, 1)
	wrapped := WrapConn(conn, p.connBPS, atomic.LoadUint64(&p.maxChunk), sizes, permits)
	go p.listenConn(sizes, permits)

	// this will not work for unix sockets or pipes, but i assume we will not serve
	// local log files over local unix socket
	p.connPool.Store(wrapped.RemoteAddr(), wrapped)

	return wrapped, nil
}

// 1: listen integers chan: chunk size to be written
// 2: on limiter.Accept - send struct{} signal to wrapped conn
// 3: on received struct{} in wrapped struct, write
func (p *Policier) listenConn(sizes <-chan uint64, permits chan<- struct{}) {
	for {
		size, ok := <-sizes
		if !ok {
			close(permits)
			return
		}

		// limit...
		limiter := p.getCurrentLimiter()

		globalBPS := atomic.LoadUint64(&p.globalBps)
		if globalBPS == 0 {
			permits <- struct{}{}
			continue
		}

		// there may be very short period when max.chunk size is not updated in connection, but limiter's burst value
		// may be decreased. With very little chance we can stuck here - so if chunk size is greater then our burst,
		// try to reserve 'burst' size, it will compensate small traffic peak
		if uint64(limiter.Burst()) < size && limiter.Burst() > 0 {
			size = uint64(limiter.Burst())
		}

		now := time.Now()
		reservation := limiter.ReserveN(time.Now(), int(size))
		time.Sleep(reservation.DelayFrom(now))
		permits <- struct{}{}
	}
}

// limit range for all existing connections
func (p *Policier) limitConnections(kbps uint64, maxChunk uint64) {
	p.connPool.Range(func(k, v interface{}) bool {
		v.(*WrappedConn).setRate(kbps, maxChunk)
		return true
	})
}

// set max chunk size for all existing connections
func (p *Policier) setConnMaxChunk(maxChunk uint64) {
	p.connPool.Range(func(k, v interface{}) bool {
		v.(*WrappedConn).calcChunk(maxChunk)
		return true
	})
}

// return pointer to current limiter
func (p *Policier) getCurrentLimiter() *rate.Limiter {
	return (*rate.Limiter)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&p.limiter))))
}

// replace current limiter with new one
func (p *Policier) setLimiter(limiter *rate.Limiter) {
	atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&p.limiter)), unsafe.Pointer(limiter))
}
