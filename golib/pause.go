package edgevpn

import "time"

// dhtRefreshPeriod is how often the DHT refreshes its routing table. The
// library default of 10 minutes costs 2 to 11 MB each time on a phone. The
// routing table only decides which DHT nodes a lookup asks first, and a
// lookup still gets fresh answers from the network.
const dhtRefreshPeriod = time.Hour

// LookForDevices starts a discovery round now, also while discovery is
// paused: it searches the current meeting point and announces this device
// there. A device that joins while every device that is on is paused is
// found this way without waiting for the hourly round. It reports false
// when the VPN is off.
func LookForDevices() bool {
	mu.Lock()
	p := currentPause
	mu.Unlock()
	if p == nil {
		return false
	}
	p.WakeUp("asked by the user")
	return true
}
