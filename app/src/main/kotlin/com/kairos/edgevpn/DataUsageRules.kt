package com.kairos.edgevpn

import org.json.JSONArray
import org.json.JSONObject
import java.util.Locale

/** The kind of network a period of traffic went over. Ethernet counts as [WIFI]. */
enum class UsageNetwork { MOBILE, WIFI }

/**
 * One reading of the counters: Android's totals for the app (since boot) and
 * the Go library's per-category stream counters (since [session] started).
 * The arrays hold five values in the Go category order: vpn, devices, dht,
 * identify, other.
 */
data class UsageSample(
    val rx: Long,
    val tx: Long,
    val session: Long,
    val streamIn: LongArray,
    val streamOut: LongArray,
    val connsDevices: Long,
    val connsOthers: Long,
)

/**
 * The sums of one network. [total] and the stream fields hold bytes in plus
 * bytes out.
 */
data class UsageColumn(
    val total: Long,
    val vpn: Long,
    val devices: Long,
    val dht: Long,
    val identify: Long,
    val other: Long,
    val connsDevices: Long,
    val connsOthers: Long,
) {
    /** What the app used beyond the VPN streams of the network, never below zero. */
    val running: Long get() = maxOf(0L, total - vpn)

    /** What the app used beyond all the counted streams, never below zero. */
    val setup: Long get() = maxOf(0L, total - vpn - devices - dht - identify - other)

    operator fun plus(o: UsageColumn) = UsageColumn(
        total + o.total, vpn + o.vpn, devices + o.devices, dht + o.dht, identify + o.identify, other + o.other,
        connsDevices + o.connsDevices, connsOthers + o.connsOthers,
    )

    operator fun minus(o: UsageColumn) = UsageColumn(
        total - o.total, vpn - o.vpn, devices - o.devices, dht - o.dht, identify - o.identify, other - o.other,
        connsDevices - o.connsDevices, connsOthers - o.connsOthers,
    )
}

/** Whether the counted streams of a period add up to more than Android's total for it. */
fun streamsExceedTotal(c: UsageColumn): Boolean =
    c.vpn + c.devices + c.dht + c.identify + c.other > c.total

/** The network of a period from the transports of the network: Wi-Fi wins over cellular, null when neither. */
fun usageNetworkOf(wifiOrEthernet: Boolean, cellular: Boolean): UsageNetwork? = when {
    wifiOrEthernet -> UsageNetwork.WIFI
    cellular -> UsageNetwork.MOBILE
    else -> null
}

private const val CATEGORIES = 5
private val EMPTY_COLUMN = UsageColumn(0, 0, 0, 0, 0, 0, 0, 0)

/** Reads the JSON of the Go library's `dataUsage()`. Null when there is no session. */
fun parseUsageSample(rx: Long, tx: Long, json: String): UsageSample? = try {
    val o = JSONObject(json)
    val session = o.optLong("session", 0)
    if (session == 0L) {
        null
    } else {
        val names = listOf("vpn", "devices", "dht", "identify", "other")
        fun read(dir: String): LongArray {
            val d = o.optJSONObject(dir)
            return LongArray(CATEGORIES) { d?.optLong(names[it], 0) ?: 0 }
        }
        UsageSample(rx, tx, session, read("in"), read("out"), o.optLong("connsDevices", 0), o.optLong("connsOthers", 0))
    }
} catch (e: org.json.JSONException) {
    null
}

/**
 * The sums per network since the last reset, and the last reading they were
 * measured from. A reading adds its differences to the last one to the column
 * of the network of the period. A first reading, a new session, or a counter
 * that went down adds nothing and only becomes the new starting point.
 */
class DataUsageBook(var resetAt: Long?, val columns: MutableMap<UsageNetwork, UsageColumn>) {
    private var last: UsageSample? = null

    fun apply(sample: UsageSample, network: UsageNetwork?) {
        val before = last
        last = sample
        if (before == null || network == null || !continues(before, sample)) return
        val diff = UsageColumn(
            total = (sample.rx + sample.tx) - (before.rx + before.tx),
            vpn = stream(sample, 0) - stream(before, 0),
            devices = stream(sample, 1) - stream(before, 1),
            dht = stream(sample, 2) - stream(before, 2),
            identify = stream(sample, 3) - stream(before, 3),
            other = stream(sample, 4) - stream(before, 4),
            connsDevices = sample.connsDevices - before.connsDevices,
            connsOthers = sample.connsOthers - before.connsOthers,
        )
        columns[network] = (columns[network] ?: EMPTY_COLUMN) + diff
    }

    /** Clears the sums and keeps the last reading, so the next one measures from it. */
    fun reset(now: Long) {
        columns.clear()
        resetAt = now
    }

    fun toJson(): String {
        val o = JSONObject()
        resetAt?.let { o.put("resetAt", it) }
        val cols = JSONObject()
        for ((network, c) in columns) {
            cols.put(
                network.name,
                JSONObject()
                    .put("total", c.total).put("vpn", c.vpn).put("devices", c.devices).put("dht", c.dht)
                    .put("identify", c.identify).put("other", c.other)
                    .put("connsDevices", c.connsDevices).put("connsOthers", c.connsOthers),
            )
        }
        o.put("columns", cols)
        last?.let { s ->
            o.put(
                "last",
                JSONObject()
                    .put("rx", s.rx).put("tx", s.tx).put("session", s.session)
                    .put("streamIn", JSONArray(s.streamIn.toList()))
                    .put("streamOut", JSONArray(s.streamOut.toList()))
                    .put("connsDevices", s.connsDevices).put("connsOthers", s.connsOthers),
            )
        }
        return o.toString()
    }

    companion object {
        /** The book stored in [json], or an empty one when it is null or unreadable. */
        fun fromJson(json: String?): DataUsageBook {
            if (json == null) return DataUsageBook(null, mutableMapOf())
            return try {
                val o = JSONObject(json)
                val columns = mutableMapOf<UsageNetwork, UsageColumn>()
                val cols = o.optJSONObject("columns")
                for (network in UsageNetwork.values()) {
                    val c = cols?.optJSONObject(network.name) ?: continue
                    columns[network] = UsageColumn(
                        c.getLong("total"), c.getLong("vpn"), c.getLong("devices"), c.getLong("dht"),
                        c.getLong("identify"), c.getLong("other"),
                        c.getLong("connsDevices"), c.getLong("connsOthers"),
                    )
                }
                val book = DataUsageBook(if (o.has("resetAt")) o.getLong("resetAt") else null, columns)
                o.optJSONObject("last")?.let { l ->
                    fun array(name: String): LongArray {
                        val a = l.getJSONArray(name)
                        return LongArray(CATEGORIES) { a.getLong(it) }
                    }
                    book.last = UsageSample(
                        l.getLong("rx"), l.getLong("tx"), l.getLong("session"),
                        array("streamIn"), array("streamOut"),
                        l.getLong("connsDevices"), l.getLong("connsOthers"),
                    )
                }
                book
            } catch (e: org.json.JSONException) {
                DataUsageBook(null, mutableMapOf())
            }
        }
    }
}

private fun stream(s: UsageSample, i: Int): Long = s.streamIn[i] + s.streamOut[i]

private fun continues(before: UsageSample, now: UsageSample): Boolean {
    if (now.session != before.session) return false
    if (now.rx < before.rx || now.tx < before.tx) return false
    if (now.connsDevices < before.connsDevices || now.connsOthers < before.connsOthers) return false
    for (i in 0 until CATEGORIES) {
        if (now.streamIn[i] < before.streamIn[i] || now.streamOut[i] < before.streamOut[i]) return false
    }
    return true
}

/** Bytes as text with 1000-based units and one decimal above bytes: "950 B", "1.5 KB", "41.2 MB". */
fun formatBytes(bytes: Long): String {
    if (bytes < 1000) return "$bytes B"
    val units = listOf("KB", "MB", "GB", "TB")
    var value = bytes / 1000.0
    var unit = 0
    while (value >= 999.95 && unit < units.size - 1) {
        value /= 1000.0
        unit++
    }
    return String.format(Locale.ROOT, "%.1f %s", value, units[unit])
}
