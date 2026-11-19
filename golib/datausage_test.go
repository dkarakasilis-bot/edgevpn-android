package edgevpn

import (
	"sync"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
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
