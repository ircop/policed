package policied

import (
	"net"
	"strings"
	"testing"
	"time"
)

type Result struct {
	AvgKBPS int
	Got     uint64
}

type testNetwork struct {
	curAddr      string
	listener     net.Listener
	stopListener chan struct{}
	sndButes     uint64
}

// run test set for currently running server
// sendBytes may be changed in runtime
func (n *testNetwork) runServer(t *testing.T, policier *Policier, bytesToSend uint64) {
	var err error
	n.stopListener = make(chan struct{})
	n.sndButes = bytesToSend
	n.listener, err = net.Listen("tcp4", ":0")
	if err != nil {
		t.Errorf("Failed to net.Listen(): %s", err.Error())
		return
	}
	n.curAddr = strings.Replace(n.listener.Addr().String(), "0.0.0.0", "127.0.0.1", -1)

	go func() {
		for {
			conn, err := policier.Wrap(n.listener.Accept())
			if err != nil {
				select {
				case <-n.stopListener:
					return
				default:
					t.Errorf("accept() failed: %s", err.Error())
					continue
				}
			}

			go func() {
				sndbuf := make([]byte, n.sndButes)
				if n, err := conn.Write(sndbuf); err != nil {
					t.Errorf("write() failed: %s (%d bytes read)", err.Error(), n)
					return
				}
				if err = conn.Close(); err != nil {
					t.Errorf("error closing server-side connection: %s", err.Error())
					return
				}
			}()
		}
	}()
}

// run test set for currently running server
func (n *testNetwork) StopServer() {
	n.stopListener <- struct{}{}
	n.listener.Close()
}

// change bytes count in runtime
func (n *testNetwork) BytesToSend(bts uint64) {
	n.sndButes = bts
}

func (n *testNetwork) runClient(t *testing.T, results chan<- Result) {
	client, err := net.Dial("tcp4", n.curAddr)
	if err != nil {
		t.Errorf("failed to dial(): %s", err.Error())
		results <- Result{AvgKBPS: -1, Got: 0}
		return
	}

	var read int
	rcvbuf := make([]byte, 4096)
	start := time.Now()
	for {
		if n, err := client.Read(rcvbuf); err != nil {
			read += n
			break // server closes connection after sending all data
		} else {
			read += n
		}
	}
	elapsed := time.Since(start)

	kbps := int(float64(read) / elapsed.Seconds() / 1024)
	results <- Result{
		Got:     uint64(read),
		AvgKBPS: kbps,
	}
}
