package policied

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"net"
	"sync"
	"testing"
	"time"
)

func transferXBytes(bytes uint64, limit uint64) (int, int, error) {
	client, s := net.Pipe()
	// assuming global rate is 100mb => bytes(100mbps)/30 = 436906
	sizes := make(chan uint64)
	permits := make(chan struct{})
	server := WrapConn(s, limit, 436906, sizes, permits)	// ~ 10 kBps
	go func(chan uint64, chan struct{}) {
		for _ = range sizes {
			permits <- struct{}{}
		}
	}(sizes, permits)


	var read int
	var wrote int
	var err error
	var wg sync.WaitGroup
	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		for {
			rcvbuf := make([]byte, 1024)
			if n, err := client.Read(rcvbuf); err == nil {
				read += n
			} else {
				read += n
				break
			}
		}
		client.Close()
		wg.Done()
	}(&wg)

	sndbuf := make([]byte, bytes)			// in-memory, for tiny tests only. For larger tests, generate/read chunks
	wrote, err = server.Write(sndbuf)
	if err != nil {
		return read, wrote, err
	}

	server.Close()
	wg.Wait()

	return read, wrote, nil
}

func runTransferTest(t *testing.T, bytes uint64, targetBps uint64) {
	start := time.Now()
	r, w, err := transferXBytes(bytes, targetBps)
	//fmt.Printf("read: %d, write: %d, passed: %d\n", r, w, bytes)
	if err != nil {
		t.Fatalf("Error during transfer %d bytes with %d bps target: %s\n", bytes, targetBps, err.Error())
	}
	elapsed := time.Since(start)
	require.Equal(t, bytes, uint64(r), "%db/%dbps: read wrong bytes count", bytes, targetBps)
	require.Equal(t, bytes, uint64(w), "%db/%dbps: wrote wrong bytes count", bytes, targetBps)

	fmt.Printf("Transfered %d bytes in %v, avg. speed is %d bps (%d kbps) with target=%d bps (%d kbps)\n", bytes, elapsed, bytes/uint64(elapsed.Seconds()), bytes/uint64(elapsed.Seconds()) / 1024, targetBps, targetBps / 1024)
}


// Simple verbose test
func TestTransfer(t *testing.T) {
	// 1 kB with limit=128 bps; short test
	runTransferTest(t, 1024, 128)

	// larger test for ~ 30 seconds with small traffic
	runTransferTest(t, 30*1024, 1024)

	// test for ~30 seconds with heavy traffic, ~90MB
	runTransferTest(t, 90*1024*1024, 3*1024*1024)

	//transfer 1GB over 100mbps link: 100mb = 12,5mB, should take smtg about 80s
	runTransferTest(t, 1000*1000*1000, 12500*1000)
}
