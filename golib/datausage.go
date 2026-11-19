package edgevpn

import (
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/metrics"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// usageCategory says what a stream's bytes were spent on.
type usageCategory int

const (
	usageVPN usageCategory = iota
	usageDevices
	usageDHT
	usageIdentify
	usageOther
)

const usageCategories = 5

// usageTotals is a snapshot of the counters, indexed by usageCategory.
type usageTotals struct {
	In, Out      [usageCategories]int64
	ConnsDevices int64
	ConnsOthers  int64
}

// usageReporter implements metrics.Reporter and sorts the bytes of every
// libp2p stream into a usageCategory. Only the stream-level Log methods count
// anything. Nothing in the app reads the Get methods, Reset or TrimIdle, so
// they return zero values or do nothing.
type usageReporter struct {
	in, out      [usageCategories]atomic.Int64
	connsDevices atomic.Int64
	connsOthers  atomic.Int64
	network      atomic.Pointer[map[peer.ID]bool]
}

var _ metrics.Reporter = (*usageReporter)(nil)

func newUsageReporter() *usageReporter {
	r := &usageReporter{}
	r.network.Store(&map[peer.ID]bool{})
	return r
}

// setNetworkPeers replaces the set of peers that belong to the user's network.
// The reporter keeps the map, so the caller must not modify it afterwards.
func (r *usageReporter) setNetworkPeers(ids map[peer.ID]bool) {
	if ids == nil {
		ids = map[peer.ID]bool{}
	}
	r.network.Store(&ids)
}

func (r *usageReporter) inNetwork(p peer.ID) bool {
	return (*r.network.Load())[p]
}

// connected counts one new connection to p.
func (r *usageReporter) connected(p peer.ID) {
	if r.inNetwork(p) {
		r.connsDevices.Add(1)
	} else {
		r.connsOthers.Add(1)
	}
}

func (r *usageReporter) classify(proto protocol.ID, p peer.ID) usageCategory {
	switch proto {
	case "/edgevpn/0.1", "/edgevpn/0.2":
		return usageVPN
	}
	if r.inNetwork(p) {
		return usageDevices
	}
	switch proto {
	case "/ipfs/kad/1.0.0":
		return usageDHT
	case "/ipfs/id/1.0.0", "/ipfs/id/push/1.0.0":
		return usageIdentify
	}
	return usageOther
}

func (r *usageReporter) totals() usageTotals {
	t := usageTotals{
		ConnsDevices: r.connsDevices.Load(),
		ConnsOthers:  r.connsOthers.Load(),
	}
	for i := range t.In {
		t.In[i] = r.in[i].Load()
		t.Out[i] = r.out[i].Load()
	}
	return t
}

func (r *usageReporter) LogSentMessage(int64) {}

func (r *usageReporter) LogRecvMessage(int64) {}

func (r *usageReporter) LogSentMessageStream(n int64, proto protocol.ID, p peer.ID) {
	r.out[r.classify(proto, p)].Add(n)
}

func (r *usageReporter) LogRecvMessageStream(n int64, proto protocol.ID, p peer.ID) {
	r.in[r.classify(proto, p)].Add(n)
}

func (r *usageReporter) GetBandwidthForPeer(peer.ID) metrics.Stats { return metrics.Stats{} }

func (r *usageReporter) GetBandwidthForProtocol(protocol.ID) metrics.Stats {
	return metrics.Stats{}
}

func (r *usageReporter) GetBandwidthTotals() metrics.Stats { return metrics.Stats{} }

func (r *usageReporter) GetBandwidthByPeer() map[peer.ID]metrics.Stats { return nil }

func (r *usageReporter) GetBandwidthByProtocol() map[protocol.ID]metrics.Stats { return nil }

func (r *usageReporter) Reset() {}

func (r *usageReporter) TrimIdle(time.Time) {}
