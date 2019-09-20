package policied

import (
	"golang.org/x/time/rate"
	"math"
	"net"
	"sync"
	"sync/atomic"
)

type Policier struct {
	globalBps		uint64
	connBps			uint64
	connPool		sync.Map

	limiter			*rate.Limiter
	maxChunk		uint64
}

// Creates new policier with given bandwidth limit
func NewPolicier(gloablKbps uint64, connKbps uint64) *Policier {
	policier := &Policier{
		connBps:	connKbps*1024,
		limiter:	rate.NewLimiter(rate.Limit(0), 0),
	}
	policier.SetGlobalRate(gloablKbps)

	return policier
}

func (p *Policier) SetGlobalRate(kbps uint64) {
	globalRate := kbps * 1024
	connRate := atomic.LoadUint64(&p.connBps)

	// There is no sence to set global limit < conn limit
	if globalRate > 0 && globalRate <  connRate {
		atomic.StoreUint64(&p.globalBps, connRate)
		p.globalBps = p.connBps
	} else {
		atomic.StoreUint64(&p.globalBps, globalRate)
	}

	// Calculate maximum chunk size.
	// We don't want single conn to occupy whole global limit
	var maxChunk uint64 = 128*1024
	if globalRate > 0 {
		maxChunk = uint64(math.Ceil(float64(p.globalBps)) / 50)
	}
	atomic.StoreUint64(&p.maxChunk, maxChunk)

	// loop over all connections and re-set max chunk
	p.connPool.Range(func(k,v interface{}) bool {
		v.(*WrappedConn).CalcChunk(maxChunk)
		return true
	})

	p.limiter = rate.NewLimiter(rate.Limit(p.globalBps), int(p.globalBps))
}

// Wrap Accept() so that we can just replace
// `..... := listener.Accept` -> `...... := policier.Wrap(listener.Accept())`
func (p *Policier) Wrap(conn net.Conn, err error) (net.Conn, error) {
	if err != nil {
		return conn, err
	}

	// wrap and do stuff
	wrapped := WrapConn(conn, p.connBps, atomic.LoadUint64(&p.maxChunk))
	go p.listenConn(wrapped)

	// this will not work for unix sockets or pipes, but i assume we will not serve local log files over local unix socket
	p.connPool.Store(wrapped.RemoteAddr(), &wrapped)

	return wrapped, nil
}

// 1: listen integers chan: chunk size to be written
// 2: on limiter.Accept - send struct{} signal to wrapped conn
// 3: on received struct{} in wrapped struct, write
func (p *Policier) listenConn(wrapped *WrappedConn) {
	//
}
