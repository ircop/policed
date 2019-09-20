package policied

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

type Result struct {
	AvgKBPS int
	Got     uint64
}

func runServer(t *testing.T, policier *Policier, addr string, sendBytes uint64) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen(): %s", err.Error())
	}
	defer listener.Close()

	for {
		conn, err := policier.Wrap(listener.Accept())
		if err != nil {
			t.Fatalf("accept() failed: %s", err.Error())
		}

		go func() {
			sndbuf := make([]byte, sendBytes)
			if n, err := conn.Write(sndbuf); err != nil {
				t.Fatalf("write failed: %s", err.Error())
			} else {
				fmt.Printf("sent %d bytes\n", n)
			}
			if err = conn.Close(); err != nil {
				t.Fatalf("error closing server-side connection: %s", err.Error())
			}
		}()
	}
}

func runClient(t *testing.T, port int, wg *sync.WaitGroup, results chan<- Result) {
	cl, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("failed to dial: %s", err.Error())
	}
	var read int
	rcvbuf := make([]byte, 4096)
	start := time.Now()
	for {
		if n, err := cl.Read(rcvbuf); err != nil {
			read += n
			break // server closes connection after sending all data
		} else {
			read += n
		}
	}
	elapsed := time.Since(start)

	results <- Result{
		Got:     uint64(read),
		AvgKBPS: read / int(elapsed.Seconds()) / 1024,
	}
	wg.Done()
}


func TestServer(t *testing.T) {
	// 4 Mbps / 1 Mbps / 512kb
	var sendBytes uint64 = 30*1024*1024
	policier := NewPolicier(1024*4, 1024)
	go runServer(t, policier, ":8899", sendBytes) // send 30 MB

	rchan := make(chan Result, 100)

	// single client:
	var wg sync.WaitGroup
	var count int
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go runClient(t, 8899, &wg, rchan)
		count = i+1
	}
	wg.Wait()

	// single client should download 30mb in ~ 30s.
	for i := 0; i < count; i++ {
		res := <-rchan
		fmt.Printf("30mb result: %d kbps avg; %d bytes got\n", res.AvgKBPS, res.Got)
		if res.AvgKBPS < 980 || res.AvgKBPS > 1070 {
			t.Fatalf("30m avg download speed wrong")
		}
		if res.Got != sendBytes {
			t.Fatalf("30mb read count wrong: %d (expected %d)", res.Got, sendBytes)
		}
	}

	// four clients should also download 30mb in ~30s (4mb total limit, 1mb per-connection)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go runClient(t, 8899, &wg, rchan)
		count = i+1
	}
	wg.Wait()
	for i := 0; i < count; i++ {
		res := <-rchan
		fmt.Printf("30mb*4 result: %d kbps avg; %d bytes got\n", res.AvgKBPS, res.Got)
		if res.AvgKBPS < 980 || res.AvgKBPS > 1070 {
			t.Fatalf("30m-4 avg download speed wrong")
		}
		if res.Got != sendBytes {
			t.Fatalf("30mb*4 read count wrong: %d (expected %d)", res.Got, sendBytes)
		}
	}

	// but 8 clients should download 30mb in ~60s, with average speed ~512kb: because of server limit
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go runClient(t, 8899, &wg, rchan)
		count = i+1
	}
	wg.Wait()
	for i := 0; i<count; i++ {
		res := <-rchan
		fmt.Printf("30mb * 8 result: %d kbps avg; %d bytes got\n", res.AvgKBPS, res.Got)
		if res.AvgKBPS < 490 || res.AvgKBPS > 535 {
			t.Fatalf("30m-8 avg download speed wrong")
		}
		if res.Got != sendBytes {
			t.Fatalf("30mb*8 read count wrong: %d (expected %d)", res.Got, sendBytes)
		}
	}
}
