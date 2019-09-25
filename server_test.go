package policied

import (
	"fmt"
	"testing"
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

	// 0 mBps / 0 mBps
	policier := NewPolicier(0, 0)
	testnet.runServer(t, policier, 5*1024*1024) // 30 mBps

	// single client:
	go testnet.runClient(t, rchan)
	res := <-rchan
	logResult("single client [0/0]", res)
	if res.AvgKBPS < 10000 {
		t.Fatalf("loopback speed shouldn't be limited")
	}

	policier.SetGlobalRate(2048) // 2 mB/s global
	go testnet.runClient(t, rchan)
	res = <-rchan
	checkResult(t, "single client [2/0]", res, 2048)

	// set global rate back to 0: check manual limits zeroing
	policier.SetGlobalRate(0)
	go testnet.runClient(t, rchan)
	res = <-rchan
	logResult("single client [0/0]", res)
	if res.AvgKBPS < 10000 {
		t.Fatalf("loopback speed shouldn't be limited")
	}

	policier.SetConnRate(1024)
	go testnet.runClient(t, rchan)
	res = <-rchan
	checkResult(t, "single client [2/1]", res, 1024)

	// set conn rate back to 0: check manual limits zeroing
	policier.SetConnRate(0)
	go testnet.runClient(t, rchan)
	res = <-rchan
	logResult("single client [0/0]", res)
	if res.AvgKBPS < 10000 {
		t.Fatalf("loopback speed shouldn't be limited")
	}
}

func TestMultiClientGlobal(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result, 100)

	policier := NewPolicier(10240, 0)
	testnet.runServer(t, policier, 10*1024*1024)

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
	testnet.runServer(t, policier, 10*1024*1024)

	for i := 0; i < 5; i++ {
		go testnet.runClient(t, rchan)
	}
	for i := 0; i < 5; i++ {
		res := <-rchan
		checkResult(t, "multi-client: [10/1]", res, 1024)
	}
}

func TestManyClients(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result, 100)

	policier := NewPolicier(25600, 0)
	testnet.runServer(t, policier, 8*1024*1024)

	for i := 0; i < 100; i++ {
		go testnet.runClient(t, rchan)
	}
	for i := 0; i < 100; i++ {
		res := <-rchan
		checkResult(t, "100 clients [25/0]", res, 256)
	}
}

func TestChangingBurstGlobalSpeed(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result)

	policier := NewPolicier(1024, 0)
	testnet.runServer(t, policier, 3*1024*1024)

	go testnet.runClient(t, rchan)
	res1 := <-rchan
	logResult("global burst: first rate", res1)

	policier.SetBurstFactor(0.5)
	go testnet.runClient(t, rchan)
	res2 := <-rchan
	logResult("global burst: second rate", res2)

	// expecting second download >10% faster
	if res2.AvgKBPS < int(float64(res1.AvgKBPS)*1.05) {
		t.Fatalf("burst: expected burst 0.5 to be >5%% faster then burst 0.005, but got %d vs %d", res1.AvgKBPS, res2.AvgKBPS)
	}
}

func TestChangingBurstConnSpeed(t *testing.T) {
	testnet := testNetwork{}
	rchan := make(chan Result)

	policier := NewPolicier(0, 1024)
	testnet.runServer(t, policier, 3*1024*1024)

	go testnet.runClient(t, rchan)
	res1 := <-rchan
	logResult("conn burst: first rate", res1)

	policier.SetBurstFactor(0.5)
	go testnet.runClient(t, rchan)
	res2 := <-rchan
	logResult("conn burst: second rate", res2)

	// expecting second download >10% faster
	if res2.AvgKBPS < int(float64(res1.AvgKBPS)*1.05) {
		t.Fatalf("burst: expected burst 0.5 to be >5%% faster then burst 0.005, but got %d vs %d", res1.AvgKBPS, res2.AvgKBPS)
	}
}
