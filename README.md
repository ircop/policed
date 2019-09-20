# usage

```go
import "...../policed"

//...

// Create and initialize policier instance
// First argument is server global rate limit, second is per-connection limit.
// Limits are in kilobytes per second, zero - no limit.
policier := policed.NewPolicier(10240, 1024)

// ...

// Wrap Accept() call with policier.
// Policier returns WrappedConn instance, which implements net.Conn interface
conn, err := policier.Wrap(listener.Accept())

// ...

// Change global rate limit:
policier.SetGlobalRate(8192)

// Change per-connection limit:
policier.SetConnRate(512)

// change individual connection limit
conn.SetRate(rate * uint64(counter))


```
