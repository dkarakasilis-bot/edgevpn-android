# Data usage screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "Data usage" screen that shows the app's data since the last reset, in mobile and Wi-Fi columns, with a drill down into what the data went to and a (?) on every line.

**Architecture:** Go classifies every libp2p stream byte into fixed categories with a custom `metrics.Reporter` and exports the session's counters as JSON. Kotlin samples those counters and Android's per-app total (`TrafficStats`) every minute and at each network change, adds the differences to the active network's column, and stores the sums. A new activity shows them.

**Tech Stack:** Go (go-libp2p v0.51.0, gomobile), Kotlin with XML views, JUnit 4 with org.json for JVM tests, Espresso for instrumented tests.

**Spec:** `docs/superpowers/specs/2026-11-16-data-usage-design.md`

## Global Constraints

- Branch `feat/data-usage`, created from `main` with `--no-track`. Commit with `git commit --no-signoff --no-gpg-sign`; the only trailer is `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.
- TDD: every code change starts with a failing test.
- Never use the owner's network key (`my.token`) anywhere. The emulator check uses `scripts/testpeer`, which makes a temporary key. Never run anything on the owner's phone.
- Text in strings, comments and commits: no em dashes, no emojis; code comments describe the current code only.
- Byte counts on screen: KB, MB or GB with one decimal (1 KB = 1000 bytes, as Android's Settings shows them).
- The two difference lines ("Keeping the VPN running", "Connection setup") never show below zero.
- Ethernet counts as Wi-Fi. A period without a network goes to neither column.
- Bytes while the VPN is off are not counted. Reset works while the VPN is off.
- No peer IDs or addresses on the screen.

## Review Focus

1. **A reboot or VPN restart between samples.** Android's counter restarts at boot and Go's at each VPN start; the next sample must only set a starting point, never add a huge or negative difference. Task 4 tests it.
2. **A network switch.** The bytes up to the switch belong to the old network's column. Task 5 samples before it tells the book the new network; Task 4 tests that the period's network is the one passed in, not the next one.
3. **Reset while the VPN runs.** Bytes before the press must not show after it. Task 5 samples just before reset; Task 4 tests that reset keeps the starting point but clears the sums.
4. **A peer that joins the ledger mid-session.** Its VPN bytes count as VPN traffic from the start, and its other bytes move to "with your devices" once the peer set refreshes. Task 2 tests both.
5. **Opening the screen with no VPN session ever run.** It shows zeros and "Since" the time of the last reset, or "Never reset" when there was none, and does not crash. Task 6 tests it.

---

### Task 1: Check what Android's per-app counter includes (gate)

A throwaway check, not merged. It decides whether the rest of the plan stands (spec, "Accuracy").

**Files:**
- Create (throwaway, on a local branch `spike/uid-traffic`, never merged): `app/src/androidTest/kotlin/com/kairos/edgevpn/UidTraffic.kt`

- [ ] **Step 1:** Write an instrumentation test `UidTraffic.printCounters` that prints `TrafficStats.getUidRxBytes(appUid)` and `getUidTxBytes(appUid)` of the app under test (`InstrumentationRegistry.getInstrumentation().targetContext.applicationInfo.uid`) as `UID_TRAFFIC rx=<n> tx=<n>`.
- [ ] **Step 2:** On emulator-5556 only, bring the app up against the test peer as `scripts/network-test` does (install, store the test network through `NetworkTestSetup`, turn on, wait for "Connected"), with `NETWORK_TEST_LONG=0 NETWORK_TEST_SWITCH=0`. Leave the test peer and the VPN running.
- [ ] **Step 3:** Read the counters (`am instrument -w -e class com.kairos.edgevpn.UidTraffic ...`), then send about 10 MB each way through the VPN: `adb -s emulator-5556 shell ping -s 1400 -i 0.01 -c 7000 10.1.0.10`. Read the counters again, and also `dumpsys netstats --uid` for the app's UID before and after.
- [ ] **Step 4:** Decide. The ping moves about 10 MB each way inside the tunnel. A difference of about 10 MB each way plus the idle background (measure it: read the counters 100 s apart without the ping) means the counter holds the app's own network bytes, which carry the tunnel traffic once. That is what the design needs, so go on. About 20 MB, or about 0, means it also includes the bytes Android credits to the shell, or leaves the tunnel out: stop and report the numbers to the owner.
- [ ] **Step 5:** Write the readings and the decision in the ledger and in the PR description later. Delete the spike branch. Stop the test peer (`scripts/testpeer down`).

### Task 2: Go reporter that classifies stream bytes

**Files:**
- Create: `golib/datausage.go`
- Test: `golib/datausage_test.go`

**Interfaces:**
- Produces:
  - `type usageCategory int` with constants `usageVPN, usageDevices, usageDHT, usageIdentify, usageOther` (in that order) and `const usageCategories = 5`.
  - `type usageReporter struct` implementing `metrics.Reporter` (`github.com/libp2p/go-libp2p/core/metrics`).
  - `func newUsageReporter() *usageReporter`
  - `func (r *usageReporter) setNetworkPeers(ids map[peer.ID]bool)` swaps the set atomically.
  - `func (r *usageReporter) connected(p peer.ID)` counts one new connection.
  - `func (r *usageReporter) totals() usageTotals`
  - `type usageTotals struct { In, Out [usageCategories]int64; ConnsDevices, ConnsOthers int64 }`

Classification, in this order: protocol `/edgevpn/0.1` or `/edgevpn/0.2` gives `usageVPN` whatever the peer; a peer in the network set gives `usageDevices`; else `/ipfs/kad/1.0.0` gives `usageDHT`; `/ipfs/id/1.0.0` and `/ipfs/id/push/1.0.0` give `usageIdentify`; everything else, including the empty protocol, gives `usageOther`. `connected` counts into `ConnsDevices` when the peer is in the set, else `ConnsOthers`.

- [ ] **Step 1: Write the failing tests**

```go
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
```

Plus `TestUsageReporterMovesAPeerThatJoinsTheNetwork` (bytes of `/meshsub/1.3.0` from a peer go to `usageOther` before `setNetworkPeers` includes it and to `usageDevices` after; earlier bytes stay where they were), `TestUsageReporterCountsNewConnections` (two `connected` calls for a network peer and one for a stranger give 2 and 1), and `TestUsageReporterIsSafeForConcurrentUse` (several goroutines log and swap the set while `go test -race` runs).

- [ ] **Step 2:** Run `cd golib && go test -run UsageReporter .` and expect a build failure (undefined names).
- [ ] **Step 3:** Implement in `golib/datausage.go`. Counters are `atomic.Int64`; the set is an `atomic.Pointer[map[peer.ID]bool]`. `LogSentMessage` and `LogRecvMessage` (connection-level, no protocol) do nothing. The `Get...` methods return zero values and `Reset` and `TrimIdle` do nothing; a doc comment says nothing in the app reads them.
- [ ] **Step 4:** Run `cd golib && go test -race -run UsageReporter .` and expect PASS.
- [ ] **Step 5:** Commit: "Count the libp2p stream bytes by what they are for".

### Task 3: Wire the reporter into the session and export `DataUsage()`

**Files:**
- Modify: `golib/transports.go` (`hostOptions`), `golib/edgevpn.go` (`Start`, session state, `Stop`)
- Modify: `golib/datausage.go`
- Test: `golib/datausage_test.go`

**Interfaces:**
- Consumes: Task 2's `usageReporter`, `usageTotals`, the category constants.
- Produces: `func DataUsage() string` (exported, gomobile) returning

```json
{"session": 1763301720000000000,
 "in":  {"vpn": 0, "devices": 0, "dht": 0, "identify": 0, "other": 0},
 "out": {"vpn": 0, "devices": 0, "dht": 0, "identify": 0, "other": 0},
 "connsDevices": 0, "connsOthers": 0}
```

`session` is `startedAt.UnixNano()` of the current session. With no session, `{"session": 0}` and nothing else.

- [ ] **Step 1: Write the failing tests:** `TestDataUsageWithoutASession` (after `Stop()`, `DataUsage()` is `{"session":0}`); `TestDataUsageReportsTheSessionCounters` (set the session state with a reporter that logged 5 bytes of `/ipfs/kad/1.0.0` in, check the JSON fields); `TestUsageServiceKeepsTheNetworkPeerSetCurrent` (the network service, given a test node whose ledger has a machine entry, puts that peer in the set within one refresh; use a short refresh interval through a package variable); `TestStreamBytesReachTheReporter` (two in-memory libp2p hosts, `libp2p.BandwidthReporter(r)` on one, a test protocol stream that writes 1000 bytes; `r.totals().Out[usageOther]` is at least 1000).
- [ ] **Step 2:** Run `cd golib && go test -run 'DataUsage|UsageService|StreamBytes' .` and expect FAIL.
- [ ] **Step 3: Implement.** `Start` creates the reporter before `hostOptions` and passes it in (`hostOptions(address string, usage metrics.Reporter)`, adding `libp2p.BandwidthReporter(usage)` when not nil); the session state holds it as `currentUsage` next to `currentMDNS`, cleared in `Stop` and `handleNodeExit`. A network service `usageService(r *usageReporter)` refreshes the set from `networkPeerIDs(ledger.CurrentData(), self)` every `usageRefresh = 10 * time.Second` and registers a `network.NotifyBundle{ConnectedF: ...}` that calls `r.connected`.
- [ ] **Step 4:** Run `cd golib && go test -race ./...` and `go vet ./...`; expect PASS.
- [ ] **Step 5:** Commit: "Export the session's data usage counters".

### Task 4: The book that keeps the sums

**Files:**
- Create: `app/src/main/kotlin/com/kairos/edgevpn/DataUsageRules.kt`
- Test: `app/src/test/kotlin/com/kairos/edgevpn/DataUsageRulesTest.kt`

**Interfaces:**
- Produces:
  - `enum class UsageNetwork { MOBILE, WIFI }`
  - `data class UsageSample(val rx: Long, val tx: Long, val session: Long, val streamIn: LongArray, val streamOut: LongArray, val connsDevices: Long, val connsOthers: Long)`, arrays of size 5 in the Go category order (vpn, devices, dht, identify, other). `fun parseUsageSample(rx: Long, tx: Long, json: String): UsageSample?` (null when `session` is 0).
  - `data class UsageColumn(val total: Long, val vpn: Long, val devices: Long, val dht: Long, val identify: Long, val other: Long, val connsDevices: Long, val connsOthers: Long)` with `val running: Long` (`max(0, total - vpn)`) and `val setup: Long` (`max(0, total - vpn - devices - dht - identify - other)`). "total" and the stream fields hold in plus out.
  - `class DataUsageBook(var resetAt: Long?, val columns: MutableMap<UsageNetwork, UsageColumn>)` with `fun apply(sample: UsageSample, network: UsageNetwork?)`, `fun reset(now: Long)`, `fun toJson(): String`, `companion fun fromJson(json: String?): DataUsageBook` (empty book for null or bad JSON), and the starting point (last sample) stored in the JSON too.
  - `fun formatBytes(bytes: Long): String`: `"0 B"`, `"950 B"`, `"1.5 KB"`, `"41.2 MB"`, `"1.3 GB"` (1000-based).

- [ ] **Step 1: Write the failing tests** in `DataUsageRulesTest`: `firstSampleOnlySetsTheStartingPoint`; `differencesGoToTheNetworkOfThePeriod` (sample 1, then sample 2 applied with `MOBILE`: the mobile column gets the differences, Wi-Fi stays zero); `aNewSessionStartsOver` (a different `session` adds nothing); `aCounterThatWentDownStartsOver` (rx lower than before adds nothing); `aPeriodWithoutANetworkIsNotCounted` (`apply(sample, null)` adds nothing but moves the starting point); `resetClearsTheSumsButKeepsTheStartingPoint` (after reset at T, the next sample adds only the bytes since the last sample before reset); `theDifferenceLinesNeverGoBelowZero` (stream bytes above the total give `running` and `setup` of 0); `theBookSurvivesJson` (round trip, and `fromJson("not json")` is empty); `formatBytesUsesOneDecimal` with the values above; `parseUsageSampleReadsTheGoJson` with the JSON from Task 3 and `{"session":0}` giving null.
- [ ] **Step 2:** Run `./binaries/gradle-8.7/bin/gradle -p . testDebugUnitTest --tests '*DataUsageRulesTest*'` and expect a compile failure.
- [ ] **Step 3:** Implement in `DataUsageRules.kt`, with `org.json` as `StatusModel.kt` uses it. No Android classes in this file, so the JVM tests run it as is.
- [ ] **Step 4:** Run the same command and expect PASS.
- [ ] **Step 5:** Commit: "Keep data usage sums per network since the last reset".

### Task 5: Sampling in the VPN service

**Files:**
- Create: `app/src/main/kotlin/com/kairos/edgevpn/DataUsageMeter.kt`
- Modify: `app/src/main/kotlin/com/kairos/edgevpn/AppSettings.kt`, `app/src/main/kotlin/com/kairos/edgevpn/EdgeVpnService.kt`
- Test: `app/src/test/kotlin/com/kairos/edgevpn/DataUsageRulesTest.kt` (the network type rule)

**Interfaces:**
- Consumes: Task 3's `Edgevpn.dataUsage()`; Task 4's book, sample and parser.
- Produces:
  - `AppSettings.dataUsage(c): String?` and `AppSettings.setDataUsage(c, json: String)`, key `"data_usage"`.
  - `object DataUsageMeter` with `fun sample(c: Context)` (reads `TrafficStats.getUidRxBytes/TxBytes(Process.myUid())` and `Edgevpn.dataUsage()`, applies to the book with the current network, stores), `fun setNetwork(c: Context, network: UsageNetwork?)` (samples first, then switches), `fun reset(c: Context)` (samples when the VPN runs, then resets and stores), `fun book(c: Context): DataUsageBook`. All synchronized; the book is loaded from `AppSettings` on first use.
  - `fun usageNetworkOf(wifiOrEthernet: Boolean, cellular: Boolean): UsageNetwork?` in `DataUsageRules.kt` (Wi-Fi wins when both are set; null when neither).

- [ ] **Step 1: Write the failing test** `usageNetworkFollowsTheTransport` for `usageNetworkOf` (wifi gives WIFI, cellular gives MOBILE, both gives WIFI, neither gives null).
- [ ] **Step 2:** Run the unit tests for it; expect a compile failure.
- [ ] **Step 3: Implement.** `EdgeVpnService`: after `Edgevpn.start` succeeds, call `DataUsageMeter.sample` once (the starting point), then every 60 s on the main handler while `isRunning`; in `onUnderlyingNetwork`, call `DataUsageMeter.setNetwork(this, usageNetworkOf(...))` from the network's `NetworkCapabilities` (`ConnectivityManager.getNetworkCapabilities(network)`) before `setUnderlyingNetworks`; in `disconnect` and `failStart`, call `DataUsageMeter.sample` before `Edgevpn.stop`. `DataUsageMeter.sample` does nothing when `Edgevpn.dataUsage()` has `session` 0.
- [ ] **Step 4:** Run `make test`; expect PASS.
- [ ] **Step 5:** Commit: "Sample the app's data usage while the VPN runs".

### Task 6: The screen

**Files:**
- Create: `app/src/main/kotlin/com/kairos/edgevpn/DataUsageActivity.kt`, `app/src/main/res/layout/activity_data_usage.xml`
- Modify: `app/src/main/res/menu/main.xml`, `app/src/main/kotlin/com/kairos/edgevpn/MainActivity.kt`, `app/src/main/AndroidManifest.xml`, `app/src/main/kotlin/com/kairos/edgevpn/ui/HelpTerm.kt`, `app/src/main/kotlin/com/kairos/edgevpn/ui/Help.kt`, `app/src/main/res/values/strings.xml`
- Test: `app/src/androidTest/kotlin/com/kairos/edgevpn/DataUsageScreenTest.kt`

**Interfaces:**
- Consumes: Task 4's `DataUsageBook`, `UsageColumn`, `formatBytes`; Task 5's `DataUsageMeter`; the existing `appendHelpIcon` and `showHelp`.
- Produces: menu item `R.id.menu_data_usage` ("Data usage", between Settings and Logs); `HelpTerm` values `USAGE_TOTAL, USAGE_VPN, USAGE_RUNNING, USAGE_DEVICES, USAGE_DHT, USAGE_IDENTIFY, USAGE_OTHER, USAGE_SETUP, USAGE_CONNECTIONS`, all with `LiveScope.PHONE`.

The layout follows the spec's sketch and the style of `activity_settings.xml`: a "Since <date, time> (<duration> ago)" line, or "Never reset", a Reset button, then one row per line with the label and its (?), and two right-aligned value columns headed "Mobile" and "Wi-Fi", then the connections line. The screen calls `DataUsageMeter.sample` when it opens and redraws from `DataUsageMeter.book` every 2 seconds while started. Reset shows a confirmation dialog ("Reset data usage?" / "The counts start again from now." / "Reset" / "Cancel").

Help copy (title / plain / tech):

| Term | Title | Plain | Tech |
|---|---|---|---|
| USAGE_TOTAL | Total | All the data this app sent and received while the VPN was on, as Android counts it. The lines below split it up. No setting changes it directly. | TrafficStats.getUidRxBytes and getUidTxBytes for the app's UID, sampled every 60 s, at each network change and when the VPN stops. Bytes while the VPN is off are not counted. |
| USAGE_VPN | Traffic through the VPN | What your apps sent to and received from your devices through the VPN. It depends on what you use the VPN for, so no setting changes it. | Stream bytes of the /edgevpn/0.1 and /edgevpn/0.2 protocols, whatever the peer. Android counts these again with IP and transport headers, which land in Connection setup. |
| USAGE_RUNNING | Keeping the VPN running | The data the VPN uses to find your devices and stay connected, even when nothing goes through it. The lines below show where it goes. | Total minus Traffic through the VPN, never below zero. |
| USAGE_DEVICES | With your devices | Keeping the list of your devices in sync and checking that each one is still there. No setting changes it yet. | Stream bytes with peers in the ledger's machines and health check buckets, other than the VPN protocols: pubsub (ledger), identify, ping. The peer set refreshes every 10 s. |
| USAGE_DHT | DHT requests | Asking computers on the internet where your devices are. They are not yours; they run the public directory (DHT) your devices use to find each other. No setting changes it yet. | /ipfs/kad/1.0.0 stream bytes with peers outside the network: routing table refreshes, discovery rounds, relay searches. |
| USAGE_IDENTIFY | Address exchange | Each new connection starts with both sides telling each other their addresses. Almost all new connections go to DHT computers. No setting changes it yet. | /ipfs/id/1.0.0 and /ipfs/id/push/1.0.0 stream bytes with peers outside the network. |
| USAGE_OTHER | Relays and other | Helpers that pass on the start of a connection, checks of how this phone can be reached, and the traffic this phone relays for others. Turning off "Relay for other devices" in Settings stops the phone relaying for others. | Every other stream byte with peers outside the network: circuit relay v2 hop and stop, AutoNAT, DCUtR, protocol negotiation. A relayed connection is counted here and again under the protocols inside it; public relays cut those after 2 minutes or 128 KiB. |
| USAGE_SETUP | Connection setup | Opening connections, encrypting them, and connections that fail. This part cannot be split by cause, but the connection counts below show where it comes from. No setting changes it yet. | Total minus every stream byte above, never below zero: TCP and QUIC handshakes, Noise and TLS, multistream negotiation, yamux framing, packet headers, failed dials. |
| USAGE_CONNECTIONS | New connections | How many connections this phone opened or accepted, to your devices and to other computers. | network.Notify ConnectedF per session, split by whether the peer is in the ledger at that moment. |

- [ ] **Step 1: Write the failing tests** in `DataUsageScreenTest`: `opensFromTheMenu` (menu "Data usage" opens a screen with "Keeping the VPN running"); `showsNeverResetWithoutAReset` (cleared prefs: "Never reset" and "0 B" in both columns); `resetAsksAndClears` (store a book with 5 MB mobile through `AppSettings.setDataUsage`, open, tap Reset, confirm, see "0 B" and "Since"); `everyHelpSheetOpens` (tap each (?) and see its title). Use the helpers in `ScreenActions.kt`.
- [ ] **Step 2:** Run `make e2e` on emulator-5556 and expect the new tests to fail.
- [ ] **Step 3:** Implement the activity, layout, menu entry, manifest entry, help terms and strings.
- [ ] **Step 4:** Run `make test` and `make e2e` on emulator-5556; expect PASS.
- [ ] **Step 5:** Commit: "Add a Data usage screen with a drill down and reset".

### Task 7: End-to-end check on the emulator

- [ ] **Step 1:** Build with `make e2e-apks GOMOBILE_TARGETS=android/arm64,android/amd64` and run `make network-test ANDROID_SERIAL=emulator-5556`; expect PASS.
- [ ] **Step 2:** With the app against the test peer as in Task 1, reset the screen, run the same ping, wait 2 minutes, and check: "Traffic through the VPN" is about 20 MB (in plus out), the Wi-Fi total is at least that, and the mobile column is 0. Then `svc wifi disable`, wait 2 minutes, and check that the mobile column grows and Wi-Fi does not. Take screenshots into `build/data-usage/`.
- [ ] **Step 3:** Write the numbers of Task 1 and this check into a short note for the PR description. Stop the test peer.
