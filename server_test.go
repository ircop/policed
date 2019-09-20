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
				t.Fatalf("write failed: %s (%d bytes read)", err.Error(), n)
			} /*else {
				fmt.Printf("sent %d bytes\n", n)
			}*/
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

func TestChangeSpeeds(t *testing.T) {
	var sendBytes uint64 = 15*1024*1024
	policier := NewPolicier(1024*4, 1024)
	go runServer(t, policier, ":8999", sendBytes) // send 30 MB

	rchan := make(chan Result, 100)
	var wg sync.WaitGroup
	wg.Add(1)
	go runClient(t, 8999, &wg, rchan)
	wg.Wait()
	r := <- rchan
	if r.AvgKBPS < 950 || r.AvgKBPS > 1100 {
		t.Fatalf("wrong initial speed: %d instead of ~ %d kbps", r.AvgKBPS, 1024)
	}
	fmt.Printf("1 client, global: 4MB, conn: 1MB: avg speed: %d kbps\n", r.AvgKBPS)




	policier.SetConnRate(512)
	wg.Add(1)
	go runClient(t, 8999, &wg, rchan)
	wg.Wait()
	r = <- rchan
	if r.AvgKBPS < 480 || r.AvgKBPS > 535 {
		t.Fatalf("wrong initial speed: %d instead of ~ %d kbps", r.AvgKBPS, 512)
	}
	fmt.Printf("1 client: change conn speed: 0.5MB: avg speed: %d kbps\n", r.AvgKBPS)


	policier.SetConnRate(1024)
	policier.SetGlobalRate(2048)

	// run 4 clients; avg. rate should be ~ 0.5MB
	wg.Add(4)
	start := time.Now()
	for i := 0; i < 4; i++ {
		go runClient(t, 8999, &wg, rchan)
	}
	wg.Wait()
	elapsed := time.Since(start)
	// 15 MB, ~0.5MB/s (2/4), should take ~30s to download
	fmt.Printf( "4 clients: change global speed: 2MB, 30MB download: %v\n", elapsed)
	if elapsed.Seconds() > 31 || elapsed.Seconds() < 29 {
		t.Fatalf("4 clients, 2MB: expected 30s for downloading, took %v", elapsed)
	}


	for i := 0; i < 4; i++ {
		r = <- rchan
		if r.AvgKBPS < 490 || r.AvgKBPS > 550 {
			t.Fatalf("Wrong speed (4clients): %d kbps, while expecting ~512", r.AvgKBPS)
		}
		fmt.Printf("4c, 2MB/s: avg speed: %d kbps\n", r.AvgKBPS)
	}
}
