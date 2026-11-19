package com.kairos.edgevpn

import android.content.Context
import android.net.TrafficStats
import android.os.Process
import android.util.Log
import edgevpn.Edgevpn

/**
 * Owns the data usage book of the app's process. The VPN service drives it while
 * the VPN runs and the usage screen reads it. Every change is stored in [AppSettings].
 */
object DataUsageMeter {
    private const val TAG = "DataUsageMeter"

    private var book: DataUsageBook? = null
    private var network: UsageNetwork? = null
    private var warnedSession = 0L

    /** The book, loaded from [AppSettings] on first use. */
    @Synchronized
    fun book(c: Context): DataUsageBook = loaded(c)

    /**
     * Reads the counters and adds what happened since the last reading to the column
     * of the current network. Does nothing without a Go session.
     */
    @Synchronized
    fun sample(c: Context) {
        val b = loaded(c)
        val uid = Process.myUid()
        val s = parseUsageSample(TrafficStats.getUidRxBytes(uid), TrafficStats.getUidTxBytes(uid), Edgevpn.dataUsage())
            ?: return
        val net = network
        val before = net?.let { b.columns[it] }
        b.apply(s, net)
        val after = net?.let { b.columns[it] }
        if (after != null) {
            val period = if (before == null) after else after - before
            if (streamsExceedTotal(period) && warnedSession != s.session) {
                warnedSession = s.session
                Log.w(TAG, "Android counted fewer bytes than the libp2p streams in one period: $period")
            }
        }
        AppSettings.setDataUsage(c, b.toJson())
    }

    /** Takes a sample for the network that ends, then makes [network] the current one. */
    @Synchronized
    fun setNetwork(c: Context, network: UsageNetwork?) {
        sample(c)
        this.network = network
    }

    /** Clears the sums. Takes a sample first, so the press is precise while the VPN runs. */
    @Synchronized
    fun reset(c: Context) {
        sample(c)
        val b = loaded(c)
        b.reset(System.currentTimeMillis())
        AppSettings.setDataUsage(c, b.toJson())
    }

    /** Drops the cached book and network, after the stored sums were erased. */
    @Synchronized
    fun forget() {
        book = null
        network = null
        warnedSession = 0
    }

    private fun loaded(c: Context): DataUsageBook =
        book ?: DataUsageBook.fromJson(AppSettings.dataUsage(c)).also { book = it }
}
