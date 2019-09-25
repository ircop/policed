package policied

import (
	"fmt"
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
	server := WrapConn(s, limit, 436906, sizes, permits) // ~ 10 kBps
	go func(chan uint64, chan struct{}) {
		for range sizes {
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

	sndbuf := make([]byte, bytes) // in-memory, for tiny tests only. For larger tests, generate/read chunks
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
	if err != nil {
		t.Errorf("Error during transfer %d bytes with %d bps target: %s\n", bytes, targetBps, err.Error())
	}
	elapsed := time.Since(start)
	if uint64(r) != bytes {
		t.Errorf("%db/%d bps: read wrong bytes count: %d, while expected %d", bytes, targetBps, r, bytes)
	}
	if uint64(w) != bytes {
		t.Errorf("%db/%d bps: write wrong bytes count: %d, while expected %d", bytes, targetBps, w, bytes)
	}

	fmt.Printf("Transferred %d bytes in %v, avg. speed is %d bps (%d kbps) with target=%d bps (%d kbps)\n",
		bytes, elapsed,
		bytes/uint64(elapsed.Seconds()),      // bps
		bytes/uint64(elapsed.Seconds())/1024, // kbps
		targetBps,
		targetBps/1024)
}


// Simple verbose test
func TestTransfer(t *testing.T) {
	testSet := []struct {
		Bts       uint64
		TargetBps uint64
	}{
		{1024, 128},							// 1kB, 128kBps
		{30*1024, 1024},						// 30kB, 1mB/s
		{90*1024*1024, 3 * 1024 * 1024},		// 90mB, ~3 mbps
	}

	for _, test := range testSet {
		runTransferTest(t, test.Bts, test.TargetBps)
	}
}
