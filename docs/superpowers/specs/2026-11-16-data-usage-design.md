# Data usage screen: design

Date: 2026-11-16. Related: issue #31 (the VPN's own data cost).

## What the user wants, and why

A screen in the app that shows how much data the VPN has used since the user last pressed reset, split into mobile data and Wi-Fi, with a drill down into what the data went to. The part that matters most is the data the VPN uses to keep itself running: the DHT, the ledger with the other devices, relays and connection setup. That cost comes even when nobody uses anything in the VPN.

The owner wants it mainly to check the effect of changes, such as the discovery pause being built for #31: reset, use the phone as usual, read the numbers. Keeping an eye on a monthly data plan is a second use.

Android shows the app's data use too (Settings > Apps > the app > Mobile data & Wi-Fi), but its UI cannot reset the count when the user wants, and it has no drill down.

## Goals

- One screen with the data since the last reset, in two columns, mobile data and Wi-Fi, plus when the reset was and how long ago.
- The total is exact: it is Android's own count of the app's bytes.
- A drill down of that total by what the data went to.
- A (?) next to every line that explains it in plain words, with technical details, and says whether a setting changes it.
- Reset whenever the user wants, precise to the moment of the press.
- The counts survive VPN restarts, app restarts and reboots.

## Non-goals

- History, such as per day or per month. Only "since reset".
- Which app sent the traffic through the VPN.
- Warnings or limits when the data use is high.
- Showing peer IDs or addresses.

## Anti-goals

- A split that looks more precise than it is. Bytes that cannot be put in a line by cause stay in their own line, named for what they are.
- A noticeable battery or CPU cost from the counting itself.

## What the screen shows

Opened from the main menu as "Data usage", next to Settings and Logs.

```
Data usage                                   [Reset]
Since 16 Nov, 14:02 (2 h 10 min ago)

                                       Mobile    Wi-Fi
Total, as Android counts it (?)        41.2 MB   12.0 MB
  Traffic through the VPN (?)           3.1 MB    5.4 MB
  Keeping the VPN running (?)          38.1 MB    6.6 MB
    With your devices (?)               0.9 MB    0.4 MB
    DHT requests (?)                   14.0 MB    2.9 MB
    Address exchange (?)                6.2 MB    1.1 MB
    Relays and other (?)                0.3 MB    0.1 MB
    Connection setup (?)               16.7 MB    2.1 MB

New connections: 3 to your devices, 2011 to other nodes (?)
```

The lines:

| Line | What it counts | Setting that changes it |
|---|---|---|
| Total, as Android counts it | every byte the app sent and received, from `TrafficStats` for the app's UID | none |
| Traffic through the VPN | stream bytes of the VPN packet protocols (`/edgevpn/0.1`, `/edgevpn/0.2`), whatever the peer | none |
| Keeping the VPN running | the total minus the line above | the lines below |
| With your devices | all other stream bytes with peers of the VPN network: the ledger (pubsub), health checks, identify, ping | none yet (#22 may add one) |
| DHT requests | `/ipfs/kad/1.0.0` stream bytes with peers outside the network | none yet (the #31 discovery pause may add one) |
| Address exchange | `/ipfs/id/1.0.0` and `/ipfs/id/push/1.0.0` stream bytes with peers outside the network | none yet |
| Relays and other | every other stream byte with peers outside the network: relay reservations, NAT checks, hole punching, and the traffic this phone relays for others | "Relay for other devices": turning it off stops the phone relaying for others |
| Connection setup | the total minus every stream byte above: TCP and encryption handshakes, protocol negotiation, muxer framing, packet headers, failed dials | none yet |

"Peers of the VPN network" are the peer IDs in the ledger's machines and health check buckets other than this phone (`networkPeerIDs` in `golib/connlog.go`).

The new connection counts sit under the table because "Connection setup" cannot be split by cause. They tell the user where that line comes from: in the emulator measurement for #31, 2011 of 2012 new connections went to nodes outside the network.

Byte counts are shown in KB, MB or GB with one decimal. "Keeping the VPN running" and "Connection setup" never show below zero. See Accuracy for when they could.

## Approaches

1. **The Android side keeps the running sums (chosen).** Go exposes counters for the current VPN session. The VPN service reads them every minute and at each network change, together with Android's total, and adds the differences to the column of the network that was active. It stores the sums in the app's preferences. Everything Android-specific stays in Kotlin, where the network type, `TrafficStats` and storage already are.
2. **Go keeps the sums.** Kotlin would pass Android's total and the network type into Go, and Go would store everything in a file. Go knows only "local network or not", so this splits the same work across the two sides worse than option 1.
3. **Android's history (`NetworkStatsManager`) for the total.** It has the mobile and Wi-Fi split built in and keeps everything, but it stores data in 2-hour buckets, so a reset could not start at the moment of the press. That spoils the before and after comparisons the owner mainly wants.

## How it works

### Go: a classifying bandwidth reporter

A new `metrics.Reporter` (`github.com/libp2p/go-libp2p/core/metrics`), set on the host with `libp2p.BandwidthReporter`, as the diagnostic branch `diag/traffic-breakdown` does with a `BandwidthCounter`. libp2p calls `LogSentMessageStream(size, protocol, peer)` and `LogRecvMessageStream(...)` for every stream read and write, with both the protocol and the peer, so the reporter puts each byte into one of the categories at once. It keeps a fixed set of counters per category and direction, nothing per peer, so its memory does not grow with the thousands of DHT nodes the phone meets in a day. `metrics.BandwidthCounter` keeps a meter per peer and per protocol, so it does not fit here.

- The VPN packet protocols go to "Traffic through the VPN" before the peer is looked at, so a device not yet in the ledger still has its VPN traffic counted there.
- Relayed connections can be counted twice: the stream to the relay (circuit relay `hop` or `stop` protocol) goes to "Relays and other", and the streams inside the relayed connection are counted again under their own protocol. The reporter does not see which connection a stream runs on, so this stays. It is small: public relays cut a relayed connection after 2 minutes or 128 KB, and edgevpn sends no VPN or ledger traffic over relayed connections. The (?) of "Relays and other" says so. When this phone relays for others, it only copies encrypted bytes between two circuit streams and sees no streams inside, so nothing is counted twice.
- It needs the set of the network's peer IDs on its hot path. A network service refreshes that set from the ledger every 10 seconds and swaps it in atomically, so the hot path only reads a map.
- The counters are atomic integers.
- The `Get...` methods of the interface return what the categories allow, or zero values. Nothing in the app calls them.
- A `network.Notify` with `ConnectedF` counts new connections, split into the network's peers and others.

One reporter per VPN session, created in `hostOptions` (`golib/transports.go`). `Start` creates one libp2p host per VPN session and keeps it until `Stop` or a failure, also across network changes, so the counters never go down within a session. A new exported function `DataUsage() string` returns JSON with a session ID (the `startedAt` time `Start` sets), the counters since the session started, in bytes received and sent per category, and the two connection counts. With no session it returns the JSON for no session.

### Kotlin: the sums

A pure class, for example `DataUsageBook`, holds the sums and applies samples. Each sample has Android's totals for the app's UID (`TrafficStats.getUidRxBytes` and `getUidTxBytes` for `Process.myUid()`), the Go snapshot, and the network type of the period that just ended. Applying a sample adds the differences since the last sample of the same VPN run to that network's column.

- The first sample of a VPN run, or one with a new Go session ID, only sets the starting point. Android's counter starts again at boot, and the VPN service is not running before it starts, so nothing is lost.
- A counter that went down is treated like a first sample.
- A period without a network is not added to either column. Ethernet counts as Wi-Fi.
- Bytes while the VPN is off are not counted: the service takes no samples then, and the next run starts from a new starting point. The app does almost nothing on the network while the VPN is off.
- Reset clears the sums and stores the time of the press, also while the VPN is off. The screen then shows zeros since that time. The next sample measures from the last reading, so a reset in the middle of a run is precise to the last sample. The screen reads a fresh sample when it opens and when the user presses reset, which makes a reset precise to the press.

`EdgeVpnService` takes a sample every 60 seconds while the VPN runs, at each change from `UnderlyingNetworkWatcher` (before it switches to the new network, so the bytes of the old network go to its column), and when the VPN stops. The network type comes from the `NetworkCapabilities` of the network the watcher reports: `TRANSPORT_CELLULAR` is mobile, `TRANSPORT_WIFI` and `TRANSPORT_ETHERNET` are Wi-Fi.

The sums are stored in the app's existing preferences (`AppSettings`), written after each sample.

The service runs in the app's process (no `android:process` in the manifest), so the screen and the service can share one in-process object that owns the book and does the sampling. The service drives it while the VPN runs. The screen asks it for a sample when it opens and before a reset when the VPN runs, and only reads the stored sums when it does not.

### The screen

A new activity in the style of `SettingsActivity` and `LogsActivity`, opened from the main menu. The (?) icons use the existing help mechanism (`Help.kt`, `HelpTerm.kt`): each line has a title, a plain text and technical details, in `strings.xml` next to the other `help_*` strings. The plain text ends with which setting changes the line, or that none does yet. Reset asks for confirmation first.

The screen refreshes every few seconds while it is open.

## Accuracy

- **The total** is Android's own count. The first step of the work, before the rest is built on it, is to check whether `TrafficStats.getUidRxBytes` for the VPN app counts only the app's own sockets, or also the bytes Android credits to the apps that sent traffic through the VPN (Android moves tunnel traffic from the VPN app to those apps in its data usage history). The emulator measurements for #31 had no traffic through the VPN, so they do not answer it. The check: run a known amount of traffic through the VPN, for example a 50 MB download from a device of the network, and compare the app's `TrafficStats` reading and the stream bytes of the VPN protocols with Settings > Apps for the same period. This can be done on the emulator against the test peer. If the reading turns out to include bytes that Android credits to other apps, or to leave out the app's own tunnel traffic, the work stops there and the design comes back to the owner with what was found.
- **The stream lines** are exact for what libp2p reports, taken from the same calls `BandwidthCounter` uses.
- **"Keeping the VPN running" and "Connection setup"** are differences. The VPN streams carry the packets plus edgevpn's framing, and Android counts the packets again with IP and TCP or QUIC headers. If Android's total turned out smaller than the sum of the stream lines in a period, which should not happen, the difference lines show zero and the log gets one WARN per VPN run.
- **The columns** follow the network the app had at each sample. Bytes in the seconds around a switch can land in the other column. Samples at each network change keep that small.

## Testing

- Go: unit tests for the reporter's classification (each protocol and peer class goes to the right category, a peer that joins the network set moves to "with your devices", counters per direction), for the connection counts, and for `DataUsage()` with and without a session. A test with two in-memory libp2p hosts that checks bytes of a known stream show up in the right category.
- Kotlin: JVM unit tests for `DataUsageBook`: a first sample sets the starting point, differences go to the right column, a new session ID or a counter that went down starts over, a period without a network is not added, reset, and the difference lines never go below zero.
- Instrumented test on the emulator: the screen opens from the menu, shows the lines, the (?) sheets open with the expected titles, and reset asks and then clears the numbers.
- On the phone, once: the accuracy check above.

## Left to the builder

- Names of classes, files and strings.
- The exact layout, within the sketch above and the style of the existing screens.
- The JSON format of `DataUsage()`.
- How the screen and the service share the sums, as long as the service is the only writer and the screen sees a fresh sample when it opens and on reset.
