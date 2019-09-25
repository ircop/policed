package policied

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

func logResult(note string, result Result) {
	fmt.Printf("%s: got %d bytes with ~%d kBps\n", note, result.Got, result.AvgKBPS)
}

func checkResult(t *testing.T, note string, result Result, expected float64) {
	logResult(note, result)
	min := expected * 0.95
	max := expected * 1.05
	if float64(result.AvgKBPS) < min || float64(result.AvgKBPS) > max {
		t.Errorf("%s: actual speed is %d kBps, while expecting %d kBps", note, result.AvgKBPS, int(expected))
	}
}

func TestSingleClient(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result, 100)

	// 4 mBps / 1 mBps
	policier := NewPolicier(0, 0)
	testnet.runServer(t, policier, 5 * 1024 * 1024 )		// 30 mBps


	// single client:
	go testnet.runClient(t, rchan)
	res := <- rchan
	logResult("single client [0/0]", res)
	if res.AvgKBPS < 10000 {
		t.Fatalf("loopback speed shouldn't be limited")
	}

	policier.SetGlobalRate(2048)		// 2 mB/s global
	go testnet.runClient(t, rchan)
	res = <- rchan
	checkResult(t, "single client [2/0]", res, 2048)

	policier.SetConnRate(1024)
	go testnet.runClient(t, rchan)
	res = <- rchan
	checkResult(t, "single client [2/1]", res, 1024)
}

func TestMultiClientGlobal(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result, 100)

	policier := NewPolicier(10240, 0)
	testnet.runServer(t, policier, 10 * 1024 * 1024)

	for i := 0; i < 5; i++ {
		go testnet.runClient(t, rchan)
	}
	for i := 0; i < 5; i++ {
		res := <-rchan
		checkResult(t, "multi-client: [10/0]", res, 2048)
	}
}

func TestMultiClientPerConn(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result, 100)

	policier := NewPolicier(10240, 1024)
	testnet.runServer(t, policier, 10 * 1024 * 1024)

	for i := 0; i < 5; i++ {
		go testnet.runClient(t, rchan)
	}
	for i := 0; i < 5; i++ {
		res := <-rchan
		checkResult(t, "multi-client: [10/1]", res, 1024)
	}
}

func runServer(t *testing.T, wg *sync.WaitGroup, policier *Policier, addr string, sendBytes uint64) {
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		//t.Fatalf("failed to listen(): %s", err.Error())
		t.Errorf("failed to listen(): %s", err.Error())
		return
	}
	defer listener.Close()
	wg.Done()

	for {
		conn, err := policier.Wrap(listener.Accept())
		if err != nil {
			t.Errorf("accept() failed: %s", err.Error())
			return
		}

		go func() {
			sndbuf := make([]byte, sendBytes)
			if n, err := conn.Write(sndbuf); err != nil {
				t.Errorf("write failed: %s (%d bytes read)", err.Error(), n)
				return
			} /*else {
				fmt.Printf("sent %d bytes\n", n)
			}*/
			if err = conn.Close(); err != nil {
				t.Errorf("error closing server-side connection: %s", err.Error())
				return
			}
		}()
	}
}

func runClient(t *testing.T, port int, wg *sync.WaitGroup, results chan<- Result) {
	defer wg.Done()
	cl, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("failed to dial: %s", err.Error())
		return
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

	kbps := int(float64(read) / elapsed.Seconds() / 1024)
	results <- Result{
		Got:     uint64(read),
		AvgKBPS: kbps,
	}
}

func TestZeroLimits(t *testing.T) {
	var sendBytes uint64 = 30 * 1024 * 1024
	policier := NewPolicier(0, 0)


	var wg sync.WaitGroup
	wg.Add(1)
	go runServer(t, &wg, policier, ":8910", sendBytes) // send 30 MB
	wg.Wait()		// wait for server to start

	rchan := make(chan Result, 100)

	wg.Add(1)
	go runClient(t, 8910, &wg, rchan)
	wg.Wait()
	r := <-rchan
	fmt.Printf("DL speed without limits: %d KB/s\n", r.AvgKBPS)
	if r.AvgKBPS < 50000 {
		t.Fatalf("Too low loopback speed without limits: %d", r.AvgKBPS)
	}

	// zero conn.speed, 1mb/s shared speed
	policier.SetGlobalRate(8192)
	wg.Add(8)
	start := time.Now()
	for i := 0; i < 8; i++ {
		go runClient(t, 8910, &wg, rchan)
	}
	wg.Wait()
	elapsed := time.Since(start)
	expected := float64(30*1024*1024) / (1024*1024)
	if elapsed.Seconds() > expected+1 || elapsed.Seconds() < expected-1 {
		t.Fatalf("wrong dl time after setting global rate: %f s. while expecting %f s.", elapsed.Seconds(), expected)
	}
	for i := 0; i < 8; i++ {
		r = <- rchan
		fmt.Printf("8 clients, 30MB, 8MB shared: %d kbps\n", r.AvgKBPS)
	}

	// test all-zeros again
	policier.SetGlobalRate(0)
	wg.Add(1)
	go runClient(t, 8910, &wg, rchan)
	wg.Wait()
	r = <-rchan
	fmt.Printf("DL speed without limits: %d KB/s\n", r.AvgKBPS)
	if r.AvgKBPS < 100000 {
		t.Fatalf("Too low loopback speed without limits: %d", r.AvgKBPS)
	}
}

func TestServer(t *testing.T) {
	// 4 Mbps / 1 Mbps / 512kb
	var sendBytes uint64 = 30 * 1024 * 1024
	policier := NewPolicier(1024*4, 1024)

	var wg sync.WaitGroup
	wg.Add(1)
	go runServer(t, &wg, policier, ":8899", sendBytes) // send 30 MB
	wg.Wait() // wait for server to start

	rchan := make(chan Result, 100)

	// single client:
	var count int
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go runClient(t, 8899, &wg, rchan)
		count = i + 1
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
		count = i + 1
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
		count = i + 1
	}
	wg.Wait()
	for i := 0; i < count; i++ {
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
	var sendBytes uint64 = 15 * 1024 * 1024
	policier := NewPolicier(1024*4, 1024)
	var wg sync.WaitGroup
	wg.Add(1)
	go runServer(t, &wg, policier, ":8999", sendBytes) // send 30 MB
	wg.Wait()

	rchan := make(chan Result, 100)
	wg.Add(1)
	go runClient(t, 8999, &wg, rchan)
	wg.Wait()
	r := <-rchan
	if r.AvgKBPS < 950 || r.AvgKBPS > 1100 {
		t.Fatalf("wrong initial speed: %d instead of ~ %d kbps", r.AvgKBPS, 1024)
	}
	fmt.Printf("1 client, global: 4MB, conn: 1MB: avg speed: %d kbps\n", r.AvgKBPS)

	policier.SetConnRate(512)
	wg.Add(1)
	go runClient(t, 8999, &wg, rchan)
	wg.Wait()
	r = <-rchan
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
	fmt.Printf("4 clients: change global speed: 2MB, 30MB download: %v\n", elapsed)
	if elapsed.Seconds() > 31 || elapsed.Seconds() < 29 {
		t.Fatalf("4 clients, 2MB: expected 30s for downloading, took %v", elapsed)
	}

	for i := 0; i < 4; i++ {
		r = <-rchan
		if r.AvgKBPS < 490 || r.AvgKBPS > 550 {
			t.Fatalf("Wrong speed (4clients): %d kbps, while expecting ~512", r.AvgKBPS)
		}
		fmt.Printf("4c, 2MB/s: avg speed: %d kbps\n", r.AvgKBPS)
	}
}

func TestIndividualRate(t *testing.T) {
	// test server, which will increase rate of each new connection by 1 mbps, starting from 1mbps
	listener, err := net.Listen("tcp", "127.0.0.1:8911")
	if err != nil {
		//t.Fatalf("failed to listen(): %s", err.Error())
		t.Fatalf("failed to listen(): %s", err.Error())
		return
	}
	//defer listener.Close()

	policier := NewPolicier(102400, 0)
	sendBytes := 30*1024*1024

	go func() {
		var rate uint64 = 1024
		counter := 1

		for {
			conn, err := policier.Wrap(listener.Accept())
			if err != nil {
				t.Errorf("accept() failed: %s", err.Error())
				return
			}
			conn.SetRate(rate * uint64(counter))
			counter++


			go func() {
				sndbuf := make([]byte, sendBytes * counter)
				if n, err := conn.Write(sndbuf); err != nil {
					t.Errorf("write failed: %s (%d bytes read)", err.Error(), n)
					return
				}
				if err = conn.Close(); err != nil {
					t.Errorf("error closing server-side connection: %s", err.Error())
					return
				}
			}()
		}
	}()


	// first:
	var wg sync.WaitGroup
	rchan := make(chan Result, 100)

	// first:
	wg.Add(1)
	go runClient(t, 8911, &wg, rchan)
	wg.Wait()
	r := <- rchan
	fmt.Printf("first speed: %d\n", r.AvgKBPS)
	if r.AvgKBPS < 990 || r.AvgKBPS > 1070 {
		t.Fatalf("wrong speed for first connection")
	}

	// second:
	wg.Add(1)
	go runClient(t, 8911, &wg, rchan)
	wg.Wait()
	r = <-rchan
	fmt.Printf("second speed: %d\n", r.AvgKBPS)
	if r.AvgKBPS < 2000 || r.AvgKBPS > 2120 {
		t.Fatalf("wrong speed for second connection")
	}

	// third:
	wg.Add(1)
	go runClient(t, 8911, &wg, rchan)
	wg.Wait()
	r = <-rchan
	fmt.Printf("third speed: %d\n", r.AvgKBPS)
	if r.AvgKBPS < 3000 || r.AvgKBPS > 3200 {
		t.Fatalf("wrong speed for third connection")
	}
}

