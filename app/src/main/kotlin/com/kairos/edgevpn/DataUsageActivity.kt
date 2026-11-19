package com.kairos.edgevpn

import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.widget.TextView
import com.google.android.material.appbar.MaterialToolbar
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import androidx.appcompat.app.AppCompatActivity
import com.kairos.edgevpn.ui.HelpTerm
import com.kairos.edgevpn.ui.appendHelpIcon
import com.kairos.edgevpn.ui.durationText
import com.kairos.edgevpn.ui.showHelp
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Shows the data the app used while the VPN ran, per network, and lets the user start the count again. */
class DataUsageActivity : AppCompatActivity() {

    private companion object {
        const val REFRESH_MS = 2000L
        val EMPTY = UsageColumn(0, 0, 0, 0, 0, 0, 0, 0)
    }

    private class Line(
        val label: Int,
        val term: HelpTerm,
        val mobile: Int,
        val wifi: Int,
        val value: (UsageColumn) -> Long,
    )

    private val lines = listOf(
        Line(R.id.usageTotal, HelpTerm.USAGE_TOTAL, R.id.usageTotalMobile, R.id.usageTotalWifi) { it.total },
        Line(R.id.usageVpn, HelpTerm.USAGE_VPN, R.id.usageVpnMobile, R.id.usageVpnWifi) { it.vpn },
        Line(R.id.usageRunning, HelpTerm.USAGE_RUNNING, R.id.usageRunningMobile, R.id.usageRunningWifi) { it.running },
        Line(R.id.usageDevices, HelpTerm.USAGE_DEVICES, R.id.usageDevicesMobile, R.id.usageDevicesWifi) { it.devices },
        Line(R.id.usageDht, HelpTerm.USAGE_DHT, R.id.usageDhtMobile, R.id.usageDhtWifi) { it.dht },
        Line(R.id.usageIdentify, HelpTerm.USAGE_IDENTIFY, R.id.usageIdentifyMobile, R.id.usageIdentifyWifi) { it.identify },
        Line(R.id.usageOther, HelpTerm.USAGE_OTHER, R.id.usageOtherMobile, R.id.usageOtherWifi) { it.other },
        Line(R.id.usageSetup, HelpTerm.USAGE_SETUP, R.id.usageSetupMobile, R.id.usageSetupWifi) { it.setup },
    )

    private val handler = Handler(Looper.getMainLooper())
    private val redraw = object : Runnable {
        override fun run() {
            DataUsageMeter.sample(this@DataUsageActivity, persist = false)
            draw()
            handler.postDelayed(this, REFRESH_MS)
        }
    }

    private var shownConnections: Pair<Long, Long>? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_data_usage)
        findViewById<MaterialToolbar>(R.id.toolbar).setNavigationOnClickListener { finish() }
        findViewById<android.view.View>(R.id.usageReset).setOnClickListener { askToReset() }
        for (line in lines) findViewById<TextView>(line.label).appendHelpIcon(line.term, ::openHelp)
    }

    override fun onStart() {
        super.onStart()
        DataUsageMeter.sample(this)
        redraw.run()
    }

    override fun onStop() {
        handler.removeCallbacks(redraw)
        super.onStop()
    }

    private fun openHelp(term: HelpTerm) {
        showHelp(this, term, live = { emptyList() })
    }

    private fun draw() {
        val book = DataUsageMeter.book(this)
        val mobile = book.columns[UsageNetwork.MOBILE] ?: EMPTY
        val wifi = book.columns[UsageNetwork.WIFI] ?: EMPTY
        findViewById<TextView>(R.id.usageSince).text = sinceText(book.resetAt)
        for (line in lines) {
            setIfChanged(findViewById(line.mobile), formatBytes(line.value(mobile)))
            setIfChanged(findViewById(line.wifi), formatBytes(line.value(wifi)))
        }
        val conns = (mobile.connsDevices + wifi.connsDevices) to (mobile.connsOthers + wifi.connsOthers)
        if (conns != shownConnections) {
            shownConnections = conns
            val view = findViewById<TextView>(R.id.usageConnections)
            view.text = getString(R.string.data_usage_connections, conns.first, conns.second)
            view.appendHelpIcon(HelpTerm.USAGE_CONNECTIONS, ::openHelp)
        }
    }

    private fun setIfChanged(view: TextView, text: String) {
        if (view.text.toString() != text) view.text = text
    }

    private fun sinceText(resetAt: Long?): String {
        if (resetAt == null) return getString(R.string.data_usage_never_reset)
        val at = Instant.ofEpochMilli(resetAt).atZone(ZoneId.systemDefault())
        val date = DateTimeFormatter.ofPattern("d MMM, HH:mm", Locale.getDefault()).format(at)
        val seconds = (System.currentTimeMillis() - resetAt) / 1000
        return getString(R.string.data_usage_since, date, durationText(seconds))
    }

    private fun askToReset() {
        MaterialAlertDialogBuilder(this)
            .setTitle(R.string.data_usage_reset_title)
            .setMessage(R.string.data_usage_reset_message)
            .setPositiveButton(R.string.data_usage_reset_confirm) { _, _ ->
                DataUsageMeter.reset(this)
                draw()
            }
            .setNegativeButton(R.string.data_usage_reset_cancel, null)
            .show()
    }
}
