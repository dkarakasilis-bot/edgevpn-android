package edgevpn

import (
	"context"
	"testing"
	"time"
)

func TestEveryLivePeerConnected(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	live, stale := now.Add(-time.Minute), now.Add(-pauseCheckInAge-time.Second)
	for _, c := range []struct {
		name      string
		checkIns  map[string]time.Time
		connected map[string]bool
		want      bool
	}{
		{"no other device", nil, nil, false},
		{"only this device", map[string]time.Time{"self": live}, map[string]bool{}, false},
		{"one live device, connected", map[string]time.Time{"a": live}, map[string]bool{"a": true}, true},
		{"one live device, not connected", map[string]time.Time{"a": live}, map[string]bool{}, false},
		{"a device that is off does not count", map[string]time.Time{"a": live, "off": stale}, map[string]bool{"a": true}, true},
		{"only devices that are off", map[string]time.Time{"off": stale}, map[string]bool{}, false},
		{"two live devices, one connected", map[string]time.Time{"a": live, "b": live}, map[string]bool{"a": true}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := everyLivePeerConnected(c.checkIns, func(id string) bool { return c.connected[id] }, now, "self")
			if got != c.want {
				t.Fatalf("got %t, want %t", got, c.want)
			}
		})
	}
}

// pauseAt returns a discoveryPause on a clock the test moves, with the
// connection state the test sets.
func pauseAt(start time.Time) (*discoveryPause, *time.Time, *bool) {
	now, connected := start, true
	p := newDiscoveryPause(func() time.Time { return now })
	p.setConnected(func() bool { return connected })
	return p, &now, &connected
}

func TestDiscoveryPausesWhileEveryDeviceIsConnected(t *testing.T) {
	p, now, connected := pauseAt(time.Unix(1_800_000_000, 0))
	if !p.Pause() {
		t.Fatal("not paused while connected to every device")
	}
	*connected = false
	if p.Pause() {
		t.Fatal("paused while a device has no connection")
	}
	*connected = true
	*now = now.Add(time.Minute)
	if !p.Pause() {
		t.Fatal("not paused again once every device is connected")
	}
}

func TestDiscoveryRunsARoundAtLeastOnceAnHour(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	p, now, _ := pauseAt(start)
	*now = start.Add(pauseMax - time.Second)
	if !p.Pause() {
		t.Fatal("not paused before the cap")
	}
	*now = start.Add(pauseMax)
	if p.Pause() {
		t.Fatal("still paused an hour after the last round")
	}
	*now = start.Add(pauseMax + time.Minute)
	if !p.Pause() {
		t.Fatal("the cap counts from the session start instead of the last round")
	}
}

func TestDiscoveryDoesNotPauseBeforeTheHostIsReady(t *testing.T) {
	p := newDiscoveryPause(time.Now)
	if p.Pause() {
		t.Fatal("paused without knowing the connections")
	}
}

func TestWakeNeverBlocks(t *testing.T) {
	p := newDiscoveryPause(time.Now)
	p.wakeUp("first")
	p.wakeUp("second")
	select {
	case <-p.wake:
	default:
		t.Fatal("no wake queued")
	}
	select {
	case <-p.wake:
		t.Fatal("two wakes queued for one round")
	default:
	}
}

func TestAWakeCountsAsARound(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	p, now, _ := pauseAt(start)
	*now = start.Add(50 * time.Minute)
	p.wakeUp("network changed")
	*now = start.Add(pauseMax + time.Minute)
	if !p.Pause() {
		t.Fatal("the cap ignores the round a wake started")
	}
}

func TestTheWatcherWakesWhenADeviceLosesItsConnection(t *testing.T) {
	p, _, connected := pauseAt(time.Unix(1_800_000_000, 0))
	p.watch()
	select {
	case <-p.wake:
		t.Fatal("woke while connected to every device")
	default:
	}
	*connected = false
	p.watch()
	select {
	case <-p.wake:
	default:
		t.Fatal("no wake when a device lost its connection")
	}
	p.watch()
	select {
	case <-p.wake:
		t.Fatal("woke again while nothing changed")
	default:
	}
}

func TestANetworkChangeStartsADiscoveryRound(t *testing.T) {
	t.Cleanup(Stop)
	calls := installNetChange(t)
	p := newDiscoveryPause(time.Now)
	setSession(newTestNode(t), true, nil, context.Background())
	mu.Lock()
	currentPause = p
	mu.Unlock()
	NetworkChanged(false)
	select {
	case <-p.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("no discovery round after a network change")
	}
	if len(calls()) != 1 {
		t.Fatalf("the network change steps ran %d times, want 1", len(calls()))
	}
}

func TestNetworkConfigRefreshesTheDHTRoutingTableLessOften(t *testing.T) {
	if got := len(networkConfig("some-token", "10.1.0.2").Discovery.DHTOptions); got != 1 {
		t.Fatalf("%d DHT options, want 1 (the routing table refresh period)", got)
	}
}

func TestTheFirstAddressEventIsTheStateAtSubscription(t *testing.T) {
	p := newDiscoveryPause(time.Now)
	p.addrs = newNetChangeDebouncer(10*time.Millisecond, p.addressesChangedWork)
	t.Cleanup(p.addrs.stop)
	p.addressEvent(true)
	time.Sleep(50 * time.Millisecond)
	select {
	case <-p.wake:
		t.Fatal("a round for the addresses the device already had")
	default:
	}
	p.addressEvent(true)
	select {
	case <-p.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("no round after the addresses changed")
	}
}

func TestAddressChangesCloseTogetherStartOneRound(t *testing.T) {
	p := newDiscoveryPause(time.Now)
	p.addrs = newNetChangeDebouncer(30*time.Millisecond, p.addressesChangedWork)
	t.Cleanup(p.addrs.stop)
	p.addressEvent(true)
	for i := 0; i < 3; i++ {
		p.addressEvent(true)
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-p.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("no round after the addresses changed")
	}
	time.Sleep(80 * time.Millisecond)
	select {
	case <-p.wake:
		t.Fatal("more than one round for changes close together")
	default:
	}
}

func TestAnAddressEventWithoutChangesStartsNoRound(t *testing.T) {
	p := newDiscoveryPause(time.Now)
	p.addrs = newNetChangeDebouncer(10*time.Millisecond, p.addressesChangedWork)
	t.Cleanup(p.addrs.stop)
	p.addressEvent(true)
	p.addressEvent(false)
	time.Sleep(50 * time.Millisecond)
	select {
	case <-p.wake:
		t.Fatal("a round for an event without changes")
	default:
	}
}

func TestTheWatcherEnforcesTheHourCap(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	p, now, _ := pauseAt(start)
	p.watch()
	*now = start.Add(pauseMax - time.Second)
	p.watch()
	select {
	case <-p.wake:
		t.Fatal("woke before the cap")
	default:
	}
	*now = start.Add(pauseMax)
	p.watch()
	select {
	case <-p.wake:
	default:
		t.Fatal("no round after an hour without one")
	}
}

func TestACheckInSeenRecentlyCountsWhateverItsTimestamp(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	var c checkInClock
	// A device whose clock is 10 minutes behind writes a new check-in.
	behind := now.Add(-10 * time.Minute)
	c.observe(map[string]time.Time{"a": behind}, now.Add(-time.Hour))
	got := c.observe(map[string]time.Time{"a": behind.Add(2 * time.Minute)}, now)
	if !now.Equal(got["a"]) {
		t.Fatalf("a check-in that changed now counts as %v, want %v", got["a"], now)
	}
	// Ten minutes later without a new check-in, its own timestamp is newer
	// than when it was seen, so that one counts.
	later := now.Add(10 * time.Minute)
	got = c.observe(map[string]time.Time{"a": behind.Add(2 * time.Minute)}, later)
	if !now.Equal(got["a"]) {
		t.Fatalf("an unchanged check-in counts as %v, want when it was seen, %v", got["a"], now)
	}
	// A device with a correct clock keeps its own timestamp.
	got = c.observe(map[string]time.Time{"b": later.Add(-time.Minute)}, later)
	if !later.Add(-time.Minute).Equal(got["b"]) {
		t.Fatalf("a current check-in counts as %v, want its timestamp", got["b"])
	}
}

func TestLookForDevicesStartsADiscoveryRound(t *testing.T) {
	t.Cleanup(Stop)
	p := newDiscoveryPause(time.Now)
	setSession(newTestNode(t), true, nil, context.Background())
	mu.Lock()
	currentPause = p
	mu.Unlock()
	if !LookForDevices() {
		t.Fatal("LookForDevices reported no session")
	}
	select {
	case <-p.wake:
	default:
		t.Fatal("no discovery round after LookForDevices")
	}
}

func TestLookForDevicesWithoutASession(t *testing.T) {
	Stop()
	if LookForDevices() {
		t.Fatal("LookForDevices reported a session after Stop")
	}
}
