# Golang rate limiter for any TCP server

This tool can be used for wrapping any tcp server listenet Accept()`s for rate limiting purposes.

Internally [rate.Limiter](https://godoc.org/golang.org/x/time/rate#NewLimiter) is used (generally, simple token buffer algorithm).



## Burst factor

Policer burst-size limit in this implementation is "the amount of time to allow a 
burst of traffic at the full line rate of a policed interface should not be lower
than 5 ms." (c) Juniper Networks

In other words, if rate limit is set to 10kBps (10000 bps) and burst factor is 0.005 
(default), actual burst size will be 50 bytes (10000 * 0.005)  

Usually you don't want to change it, but here is it's principe: the lower the burst factor is,
the more accurate is rate limiting, but the more resources it consumes. And vice versa. 

If your desired rate limits are low enough, like 128kB-1mB per second, default burst is OK for you. 

But if your rates are something like 10mB/s and higher, you may want to increase burst, maybe, to 0.05

[More about burst](https://www.juniper.net/documentation/en_US/junos/topics/concept/policer-mx-m120-m320-burstsize-determining.html)


# usage

### Initialization
```go
import "github.com/ircop/policed"

// ...

// Create and initialize new policier instance. 
// First argument is global rate limit, second is per-connection limit (in kB per second)
policier := policed.NewPolicier(10240, 1024)

// Wrap Accept() call with policier.
// Policier returns WrappedConn instance, which implements net.Conn interface
conn, err := policier.Wrap(listener.Accept())

// ... do stuff ... 


// Change global rate limit:
policier.SetGlobalRate(8192)

// Change per-connection limit:
policier.SetConnRate(512)

// change individual connection limit
conn.SetRate(rate * uint64(counter))
```


### Changing parameters on the fly

```go
// change global speed (kB/s).
// all currently established connections will also be affected.
policier.SetGlobalRate(10240)


// change per-connection speed limit (kB/s).
// all currently established connections will also be affected.
policier.SetConnRate(1024)


// Change burst factor
policier.SetBurstFactor(0.05)

// set individual rate limit for connection (kB/s)
conn.SetRate(512)
```

