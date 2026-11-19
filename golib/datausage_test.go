package edgevpn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/mudler/edgevpn/pkg/blockchain"
	edgeprotocol "github.com/mudler/edgevpn/pkg/protocol"
	"github.com/mudler/edgevpn/pkg/types"
)

func TestUsageReporterClassifiesStreamBytes(t *testing.T) {
	r := newUsageReporter()
	mine, stranger := peer.ID("mine"), peer.ID("stranger")
	r.setNetworkPeers(map[peer.ID]bool{mine: true})
	r.LogRecvMessageStream(100, "/edgevpn/0.2", stranger)
	r.LogSentMessageStream(10, "/edgevpn/0.1", mine)
	r.LogRecvMessageStream(200, "/meshsub/1.3.0", mine)
	r.LogRecvMessageStream(300, "/ipfs/kad/1.0.0", stranger)
	r.LogSentMessageStream(40, "/ipfs/kad/1.0.0", mine)
	r.LogRecvMessageStream(50, "/ipfs/id/push/1.0.0", stranger)
	r.LogSentMessageStream(60, "/ipfs/id/1.0.0", stranger)
	r.LogRecvMessageStream(7, "/libp2p/circuit/relay/0.2.0/hop", stranger)
	r.LogRecvMessageStream(3, "", stranger)
	got := r.totals()
	want := usageTotals{
		In:  [usageCategories]int64{usageVPN: 100, usageDevices: 200, usageDHT: 300, usageIdentify: 50, usageOther: 10},
		Out: [usageCategories]int64{usageVPN: 10, usageDevices: 40, usageIdentify: 60},
	}
	if got != want {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
}

func TestUsageReporterMovesAPeerThatJoinsTheNetwork(t *testing.T) {
	r := newUsageReporter()
	p := peer.ID("p")
	r.LogRecvMessageStream(5, "/meshsub/1.3.0", p)
	r.setNetworkPeers(map[peer.ID]bool{p: true})
	r.LogRecvMessageStream(8, "/meshsub/1.3.0", p)
	got := r.totals()
	if got.In[usageOther] != 5 || got.In[usageDevices] != 8 {
		t.Fatalf("totals = %+v, want 5 other and 8 devices", got)
	}
}

func TestUsageReporterCountsNewConnections(t *testing.T) {
	r := newUsageReporter()
	mine, stranger := peer.ID("mine"), peer.ID("stranger")
	r.setNetworkPeers(map[peer.ID]bool{mine: true})
	r.connected(mine)
	r.connected(mine)
	r.connected(stranger)
	got := r.totals()
	if got.ConnsDevices != 2 || got.ConnsOthers != 1 {
		t.Fatalf("conns = %d devices, %d others, want 2 and 1", got.ConnsDevices, got.ConnsOthers)
	}
}

func TestUsageReporterIsSafeForConcurrentUse(t *testing.T) {
	r := newUsageReporter()
	p := peer.ID("p")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				r.LogRecvMessageStream(1, "/meshsub/1.3.0", p)
				r.LogSentMessageStream(1, "/edgevpn/0.2", p)
				r.connected(p)
				_ = r.totals()
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				r.setNetworkPeers(map[peer.ID]bool{p: j%2 == 0})
			}
		}()
	}
	wg.Wait()
	got := r.totals()
	if got.In[usageDevices]+got.In[usageOther] != 4000 || got.Out[usageVPN] != 4000 {
		t.Fatalf("totals = %+v, lost updates", got)
	}
}

func TestDataUsageWithoutASession(t *testing.T) {
	Stop()
	if got := DataUsage(); got != `{"session":0}` {
		t.Fatalf("DataUsage() = %s, want {\"session\":0}", got)
	}
}

func TestDataUsageReportsTheSessionCounters(t *testing.T) {
	t.Cleanup(Stop)
	r := newUsageReporter()
	r.LogRecvMessageStream(5, "/ipfs/kad/1.0.0", peer.ID("stranger"))
	r.LogSentMessageStream(7, "/edgevpn/0.2", peer.ID("stranger"))
	r.connected(peer.ID("stranger"))
	started := time.Now()
	mu.Lock()
	currentUsage, startedAt = r, started
	mu.Unlock()

	var got struct {
		Session      int64            `json:"session"`
		In           map[string]int64 `json:"in"`
		Out          map[string]int64 `json:"out"`
		ConnsDevices int64            `json:"connsDevices"`
		ConnsOthers  int64            `json:"connsOthers"`
	}
	if err := json.Unmarshal([]byte(DataUsage()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Session != started.UnixNano() {
		t.Errorf("session = %d, want %d", got.Session, started.UnixNano())
	}
	for _, k := range []string{"vpn", "devices", "dht", "identify", "other"} {
		if _, ok := got.In[k]; !ok {
			t.Errorf("in has no %q: %v", k, got.In)
		}
		if _, ok := got.Out[k]; !ok {
			t.Errorf("out has no %q: %v", k, got.Out)
		}
	}
	if got.In["dht"] != 5 || got.Out["vpn"] != 7 || got.ConnsOthers != 1 || got.ConnsDevices != 0 {
		t.Errorf("counters = %+v", got)
	}
}

func TestUsageServiceKeepsTheNetworkPeerSetCurrent(t *testing.T) {
	old := usageRefresh
	usageRefresh = 20 * time.Millisecond
	t.Cleanup(func() { usageRefresh = old })

	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	other, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })

	ledger := blockchain.New(io.Discard, &blockchain.MemoryStore{})
	r := newUsageReporter()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runUsage(ctx, h, ledger, r)

	ledger.Add(edgeprotocol.MachinesLedgerKey, map[string]interface{}{
		"10.1.0.10": types.Machine{PeerID: other.ID().String()},
	})
	deadline := time.Now().Add(2 * time.Second)
	for !r.inNetwork(other.ID()) {
		if time.Now().After(deadline) {
			t.Fatal("the machine's peer did not enter the network set")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := h.Connect(ctx, peer.AddrInfo{ID: other.ID(), Addrs: other.Addrs()}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for r.totals().ConnsDevices == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the new connection was not counted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStreamBytesReachTheReporter(t *testing.T) {
	r := newUsageReporter()
	a, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.BandwidthReporter(r))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	const proto protocol.ID = "/test/bytes/1.0.0"
	b.SetStreamHandler(proto, func(s network.Stream) {
		io.Copy(io.Discard, s)
		s.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatal(err)
	}
	s, err := a.NewStream(ctx, b.ID(), proto)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(make([]byte, 1000)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	deadline := time.Now().Add(5 * time.Second)
	for r.totals().Out[usageOther] < 1000 {
		if time.Now().After(deadline) {
			t.Fatalf("totals = %+v, want at least 1000 bytes out as other", r.totals())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDataUsageSurvivesANodeFailure(t *testing.T) {
	t.Cleanup(func() { Stop(); ClearLogs() })
	n := newTestNode(t)
	r := newUsageReporter()
	r.LogRecvMessageStream(5, "/ipfs/kad/1.0.0", peer.ID("stranger"))
	started := time.Now()
	mu.Lock()
	currentNode, currentUsage, startedAt = n, r, started
	mu.Unlock()

	handleNodeExit(n, func() {}, errors.New("boom"))

	var got struct {
		Session int64            `json:"session"`
		In      map[string]int64 `json:"in"`
	}
	if err := json.Unmarshal([]byte(DataUsage()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Session != started.UnixNano() || got.In["dht"] != 5 {
		t.Errorf("after the node failed, DataUsage() = %s", DataUsage())
	}
	Stop()
	if got := DataUsage(); got != `{"session":0}` {
		t.Errorf("after Stop, DataUsage() = %s", got)
	}
}
