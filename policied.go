package policied

import (
	"errors"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/time/rate"
)

var ErrWrongBurstFactor = errors.New("burst factor out of range. Should be 1 < factor < 0.001")

type Policier struct {
	globalBps   uint64
	connBPS     uint64
	connPool    sync.Map
	burstFactor BurstFactor

	limiter  *rate.Limiter
	maxChunk uint64
}

type BurstFactor struct {
	factor float64
	mu     sync.Mutex
}

// Creates new policier with given bandwidth limit in KBps (kiloBYtes)
// If bandwidth is zero, there is no limit
func NewPolicier(gloablKBPS uint64, connKBPS uint64) *Policier {
	policier := &Policier{
		burstFactor: BurstFactor{factor: 0.005},
		connBPS:     connKBPS * 1024,
		limiter:     rate.NewLimiter(rate.Limit(0), 0),
	}
	policier.SetGlobalRate(gloablKBPS)

	return policier
}

// SetConnRate setc rate limit per each connection, including all currently existent
func (p *Policier) SetConnRate(kbps uint64) {
	connRate := kbps * 1024

	// check that conn.rate is <= global rate
	globalRate := atomic.LoadUint64(&p.globalBps)
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

	p.setLimiterParams(globalRate, p.burstFactor.Get())
}

// SetBurstFactor sets... a burst factor :) which is, actually, the amount of time to allow a burst of traffic
// at the full line rate of a policed connection.
// I.e if rate limit is 10000 (bytes per second), and burst factor is 0.005 (s, or 5ms), actual burst will be
// 10000 * 0.005 => 50 bytes
// Usually you do not need to adjust it, but you may want it with very high or very low limit rates.
// Value should be between 0 and 1000. Default is 5.
// The lower the burst factor, the more accurate is rate limiting, but the more resources it consumes. And vice versa.
func (p *Policier) SetBurstFactor(factor float64) error {
	if err := p.burstFactor.Set(factor); err != nil {
		return err
	}
	globalRate := atomic.LoadUint64(&p.globalBps)

	p.setLimiterParams(globalRate, factor)

	return nil
}

// (re)calculate max chunk
// replace limiter
// set conn's parameters
func (p *Policier) setLimiterParams(globalRate uint64, burstFactor float64) {
	// calculate max chunk. If global rate is 0, max. chunk is also 0
	var maxChunk uint64
	limit := rate.Inf
	if globalRate > 0 {
		maxChunk = uint64(math.Ceil(float64(globalRate) * burstFactor))
		limit = rate.Limit(globalRate)
	}

	atomic.StoreUint64(&p.maxChunk, maxChunk)
	p.setLimiter(rate.NewLimiter(limit, int(maxChunk)))

	p.setConnMaxChunk(maxChunk)
}

// Wrap Accept() so that we can just replace
// `..... := listener.Accept` -> `...... := policier.Wrap(listener.Accept())`
func (p *Policier) Wrap(conn net.Conn, err error) (*WrappedConn, error) {
	if err != nil {
		return nil, err
	}

	// wrap and do stuff
	wrapped := WrapConn(conn, p.connBPS, atomic.LoadUint64(&p.maxChunk), &p.burstFactor, p.getCurrentLimiter)

	// this will not work for unix sockets or pipes, but i assume we will not serve
	// local log files over local unix socket
	p.connPool.Store(wrapped.RemoteAddr(), wrapped)

	return wrapped, nil
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

func (b *BurstFactor) Set(factor float64) error {
	if factor < 0.001 || factor > 1 {
		return ErrWrongBurstFactor
	}
	b.mu.Lock()
	b.factor = factor
	b.mu.Unlock()

	return nil
}
func (b *BurstFactor) Get() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.factor
}
