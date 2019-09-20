package policied

import (
	"golang.org/x/time/rate"
	"net"
	"sync"
)

type Policier struct {
	globalBps		uint64
	connBps			uint64
	connPool		sync.Map

	limiter			*rate.Limiter
	chunkSize		uint32
}

// Creates new policier with given bandwidth limit
func NewPolicier(gloablKbps uint64, connKbps uint64) *Policier {
	policier := &Policier{
		connBps:	connKbps*1024,
		limiter:	rate.NewLimiter(rate.Limit(0), 0),
		chunkSize:	4096,		// 4kb
	}
	policier.SetGlobalRate(gloablKbps)

	return policier
}

/*func (p *Policier) SetChunkSize(size uint32) {
	atomic.StoreUint32(&p.chunkSize, size)
}*/

func (p *Policier) SetGlobalRate(kbps uint64) {
	p.globalBps = kbps * 1024

	// There is no sence to set global limit < conn limit
	if p.globalBps < p.connBps {
		p.globalBps = p.connBps
	}

	p.limiter = rate.NewLimiter(rate.Limit(p.globalBps), int(p.globalBps))
}

// Wrap Accept() so that we can just replace
// `..... := listener.Accept` -> `...... := policier.Wrap(listener.Accept())`
func (p *Policier) Wrap(conn net.Conn, err error) (net.Conn, error) {
	if err != nil {
		return conn, err
	}

	// wrap and do stuff
	wrapped := WrapConn(conn, p.connBps, make(chan uint32), make(chan struct{}))
	go p.listenConn(wrapped)

	// this will not work for unix sockets, but i assume we will not serve local files over local socket
	p.connPool.Store(wrapped.RemoteAddr(), &wrapped)


	return wrapped, nil
}

func (p *Policier) listenConn(wrapped *WrappedConn) {
	//
}

/*
listen all conn's
goroutine per conn:
	listen until conn.chan is OK

*/
