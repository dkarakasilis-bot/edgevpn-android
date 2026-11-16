package edgevpn

import (
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/edgevpn/pkg/blockchain"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/edgevpn/pkg/protocol"
)

const (
	// pauseCheckInAge is how recent a device's check-in must be for the
	// pause to wait for a connection to it. Devices check in every 2
	// minutes (aliveAnnounce, the CLI default too), so a device that missed
	// two check-ins is taken as off.
	pauseCheckInAge = 5 * time.Minute

	// pauseMax is the longest time without a discovery round. It bounds
	// what a wrong "connected to everyone" costs.
	pauseMax = time.Hour

	// pauseAddrDelay is how long the addresses have to stay the same after
	// a change before a discovery round announces them. A relay
	// reservation or a network switch changes them several times within a
	// few seconds.
	pauseAddrDelay = 30 * time.Second

	// pauseWatchInterval is how often the watcher checks whether a device
	// lost its connection while discovery is paused.
	pauseWatchInterval = 30 * time.Second

	// dhtRefreshPeriod is how often the DHT refreshes its routing table.
	// The library default of 10 minutes costs 2 to 11 MB each time on a
	// phone. The routing table only decides which DHT nodes a lookup asks
	// first, and a lookup still gets fresh answers from the network.
	dhtRefreshPeriod = time.Hour
)

// discoveryPause holds back edgevpn's regular DHT discovery rounds while the
// phone has a connection to every device of the network that is on. A round
// runs anyway at least every pauseMax, and wakeUp starts one at once. edgevpn
// asks Pause before each regular round and listens on wake.
type discoveryPause struct {
	now  func() time.Time
	wake chan struct{}
	// addrs delays the round for an address change until the addresses
	// settle.
	addrs *netChangeDebouncer

	mu        sync.Mutex
	connected func() bool
	lastRound time.Time
	paused    bool
	// watched is what the last watch saw.
	watched bool
	// sawAddrs is true once the first address event arrived.
	sawAddrs bool
}

func newDiscoveryPause(now func() time.Time) *discoveryPause {
	p := &discoveryPause{now: now, wake: make(chan struct{}, 1), lastRound: now()}
	p.addrs = newNetChangeDebouncer(pauseAddrDelay, p.addressesChangedWork)
	return p
}

// addressEvent handles an EvtLocalAddressesUpdated. The event bus hands a new
// subscriber the last event at once, which holds the addresses the device
// has had since before the first round, so the first event is skipped.
func (p *discoveryPause) addressEvent(diffs bool) {
	p.mu.Lock()
	first := !p.sawAddrs
	p.sawAddrs = true
	p.mu.Unlock()
	if first || !diffs {
		return
	}
	p.addrs.trigger(false)
}

func (p *discoveryPause) addressesChangedWork(bool) {
	p.wakeUp("this device's addresses changed")
}

// setConnected gives the pause the check it needs, once the host exists.
func (p *discoveryPause) setConnected(f func() bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = f
}

// Pause reports whether the next regular discovery round can be skipped.
func (p *discoveryPause) Pause() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	pause := p.connected != nil && p.connected() && now.Sub(p.lastRound) < pauseMax
	if !pause {
		p.lastRound = now
	}
	if pause != p.paused {
		p.paused = pause
		if pause {
			emit("INFO ", "discovery paused: connected to every device that is on")
		} else {
			emit("INFO ", "discovery resumed")
		}
	}
	return pause
}

// wakeUp asks edgevpn for a discovery round now. A round already asked for
// and not started yet covers this one too.
func (p *discoveryPause) wakeUp(reason string) {
	p.mu.Lock()
	p.lastRound = p.now()
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
		emit("INFO ", "discovery round now: "+reason)
	default:
	}
}

// watch wakes discovery up when a device lost its connection since the last
// watch, so that the phone looks for it without waiting for the next regular
// round.
//
// It also starts the round that the hour cap asks for. Pause only sees the
// cap on a regular tick, and those come up to 18 minutes apart.
func (p *discoveryPause) watch() {
	p.mu.Lock()
	ok := p.connected != nil && p.connected()
	lost := p.watched && !ok
	p.watched = ok
	overdue := p.now().Sub(p.lastRound) >= pauseMax
	p.mu.Unlock()
	switch {
	case lost:
		p.wakeUp("a device has no connection")
	case overdue:
		p.wakeUp("an hour without a discovery round")
	}
}

// everyLivePeerConnected reports whether at least one device other than self
// checked in within pauseCheckInAge of now, and connected reports true for
// each such device. checkIns maps peer IDs to their last check-in.
func everyLivePeerConnected(checkIns map[string]time.Time, connected func(string) bool, now time.Time, self string) bool {
	live := 0
	for id, at := range checkIns {
		if id == self || now.Sub(at) > pauseCheckInAge {
			continue
		}
		if !connected(id) {
			return false
		}
		live++
	}
	return live > 0
}

// checkInClock tells when this device saw each peer's check-in change. A
// device whose clock is behind writes check-ins that look old, and a device
// with a long check-in interval writes them rarely. Counting the local time
// of a change as well keeps both from looking off while they check in.
type checkInClock struct {
	mu   sync.Mutex
	last map[string]time.Time
	seen map[string]time.Time
}

// observe returns, for each peer of checkIns, the later of its check-in and
// the time now at which this device first saw that check-in. A check-in
// already there at the first observe counts by its own timestamp only.
func (c *checkInClock) observe(checkIns map[string]time.Time, now time.Time) map[string]time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last, c.seen = map[string]time.Time{}, map[string]time.Time{}
	}
	out := make(map[string]time.Time, len(checkIns))
	for id, at := range checkIns {
		prev, known := c.last[id]
		if known && !prev.Equal(at) {
			c.seen[id] = now
		}
		c.last[id] = at
		out[id] = at
		if s := c.seen[id]; s.After(at) {
			out[id] = s
		}
	}
	return out
}

// checkInTimes returns the last check-in of each peer in the ledger data.
// Entries that do not parse are left out.
func checkInTimes(data map[string]map[string]blockchain.Data) map[string]time.Time {
	out := map[string]time.Time{}
	for id, v := range data[protocol.HealthCheckKey] {
		var s string
		if v.Unmarshal(&s) != nil {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			out[id] = t
		}
	}
	return out
}

// pauseService returns the network service that feeds p from the session's
// host and ledger: the connection check, a watch every pauseWatchInterval,
// and a wake once the phone's own addresses change and settle, because the
// DHT record of this device holds the addresses it had when it was
// announced.
func pauseService(p *discoveryPause) node.NetworkService {
	return func(ctx context.Context, _ node.Config, n *node.Node, ledger *blockchain.Ledger) error {
		h := n.Host()
		self := h.ID().String()
		var clock checkInClock
		p.setConnected(func() bool {
			now := time.Now()
			return everyLivePeerConnected(clock.observe(checkInTimes(ledger.CurrentData()), now), func(id string) bool {
				pid, err := peer.Decode(id)
				return err == nil && h.Network().Connectedness(pid) == network.Connected
			}, now, self)
		})
		sub, err := h.EventBus().Subscribe(new(event.EvtLocalAddressesUpdated))
		if err != nil {
			emit("WARN ", "cannot follow this device's addresses for the discovery pause: "+err.Error())
		}
		go func() {
			t := time.NewTicker(pauseWatchInterval)
			defer t.Stop()
			var addrs <-chan interface{}
			if sub != nil {
				defer sub.Close()
				addrs = sub.Out()
			}
			defer p.addrs.stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					p.watch()
				case e, ok := <-addrs:
					if !ok {
						addrs = nil
						continue
					}
					if u, isUpdate := e.(event.EvtLocalAddressesUpdated); isUpdate {
						p.addressEvent(u.Diffs)
					}
				}
			}
		}()
		return nil
	}
}
