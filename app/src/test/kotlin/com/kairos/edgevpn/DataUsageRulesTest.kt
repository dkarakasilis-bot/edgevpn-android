package com.kairos.edgevpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DataUsageRulesTest {

    private fun sample(
        rx: Long = 0,
        tx: Long = 0,
        session: Long = 1,
        streamIn: List<Long> = listOf(0, 0, 0, 0, 0),
        streamOut: List<Long> = listOf(0, 0, 0, 0, 0),
        connsDevices: Long = 0,
        connsOthers: Long = 0,
    ) = UsageSample(rx, tx, session, streamIn.toLongArray(), streamOut.toLongArray(), connsDevices, connsOthers)

    private val zero = UsageColumn(0, 0, 0, 0, 0, 0, 0, 0)

    @Test
    fun firstSampleOnlySetsTheStartingPoint() {
        val book = DataUsageBook(null, mutableMapOf())
        book.apply(sample(rx = 1000, tx = 500), UsageNetwork.MOBILE)
        assertTrue(book.columns.values.all { it == zero })
    }

    @Test
    fun differencesGoToTheNetworkOfThePeriod() {
        val book = DataUsageBook(null, mutableMapOf())
        book.apply(
            sample(rx = 1000, tx = 500, streamIn = listOf(10, 0, 0, 0, 0), streamOut = listOf(5, 0, 0, 0, 0)),
            UsageNetwork.MOBILE,
        )
        book.apply(
            sample(
                rx = 1900, tx = 800,
                streamIn = listOf(110, 20, 3, 0, 1), streamOut = listOf(55, 10, 2, 1, 0),
                connsDevices = 2, connsOthers = 3,
            ),
            UsageNetwork.MOBILE,
        )
        val mobile = book.columns[UsageNetwork.MOBILE]
        assertEquals(UsageColumn(1200, 150, 30, 5, 1, 1, 2, 3), mobile)
        assertEquals(zero, book.columns[UsageNetwork.WIFI] ?: zero)
    }

    @Test
    fun aNewSessionStartsOver() {
        val book = DataUsageBook(null, mutableMapOf())
        book.apply(sample(rx = 100, session = 1), UsageNetwork.WIFI)
        book.apply(sample(rx = 900, session = 2), UsageNetwork.WIFI)
        assertEquals(zero, book.columns[UsageNetwork.WIFI] ?: zero)
        book.apply(sample(rx = 950, session = 2), UsageNetwork.WIFI)
        assertEquals(50L, book.columns[UsageNetwork.WIFI]?.total)
    }

    @Test
    fun aCounterThatWentDownStartsOver() {
        val book = DataUsageBook(null, mutableMapOf())
        book.apply(sample(rx = 1000), UsageNetwork.WIFI)
        book.apply(sample(rx = 10), UsageNetwork.WIFI)
        assertEquals(zero, book.columns[UsageNetwork.WIFI] ?: zero)
        book.apply(sample(rx = 40), UsageNetwork.WIFI)
        assertEquals(30L, book.columns[UsageNetwork.WIFI]?.total)
    }

    @Test
    fun aPeriodWithoutANetworkIsNotCounted() {
        val book = DataUsageBook(null, mutableMapOf())
        book.apply(sample(rx = 100), UsageNetwork.MOBILE)
        book.apply(sample(rx = 600), null)
        assertTrue(book.columns.values.all { it == zero })
        book.apply(sample(rx = 650), UsageNetwork.MOBILE)
        assertEquals(50L, book.columns[UsageNetwork.MOBILE]?.total)
    }

    @Test
    fun resetClearsTheSumsButKeepsTheStartingPoint() {
        val book = DataUsageBook(null, mutableMapOf())
        book.apply(sample(rx = 100), UsageNetwork.MOBILE)
        book.apply(sample(rx = 300), UsageNetwork.MOBILE)
        book.reset(42L)
        assertEquals(42L, book.resetAt)
        assertTrue(book.columns.values.all { it == zero })
        book.apply(sample(rx = 340), UsageNetwork.MOBILE)
        assertEquals(40L, book.columns[UsageNetwork.MOBILE]?.total)
    }

    @Test
    fun theDifferenceLinesNeverGoBelowZero() {
        val column = UsageColumn(100, 150, 10, 0, 0, 0, 0, 0)
        assertEquals(0L, column.running)
        assertEquals(0L, column.setup)
        val normal = UsageColumn(1000, 600, 100, 50, 10, 20, 0, 0)
        assertEquals(400L, normal.running)
        assertEquals(220L, normal.setup)
    }

    @Test
    fun theBookSurvivesJson() {
        val book = DataUsageBook(7L, mutableMapOf())
        book.apply(sample(rx = 100, streamIn = listOf(1, 2, 3, 4, 5)), UsageNetwork.MOBILE)
        book.apply(
            sample(rx = 400, tx = 20, streamIn = listOf(11, 12, 13, 14, 15), connsOthers = 4),
            UsageNetwork.WIFI,
        )
        val back = DataUsageBook.fromJson(book.toJson())
        assertEquals(7L, back.resetAt)
        assertEquals(book.columns, back.columns)
        back.apply(sample(rx = 410, tx = 20, streamIn = listOf(11, 12, 13, 14, 15), connsOthers = 4), UsageNetwork.WIFI)
        assertEquals(book.columns[UsageNetwork.WIFI]!!.total + 10, back.columns[UsageNetwork.WIFI]!!.total)

        val empty = DataUsageBook.fromJson("not json")
        assertNull(empty.resetAt)
        assertTrue(empty.columns.values.all { it == zero })
        assertNotNull(DataUsageBook.fromJson(null))
    }

    @Test
    fun formatBytesUsesOneDecimal() {
        assertEquals("0 B", formatBytes(0))
        assertEquals("950 B", formatBytes(950))
        assertEquals("1.5 KB", formatBytes(1500))
        assertEquals("41.2 MB", formatBytes(41_200_000))
        assertEquals("1.3 GB", formatBytes(1_300_000_000))
    }

    @Test
    fun parseUsageSampleReadsTheGoJson() {
        val json = """{"session":1763301720000000000,"in":{"vpn":1,"devices":2,"dht":3,"identify":4,"other":5},""" +
            """"out":{"vpn":6,"devices":7,"dht":8,"identify":9,"other":10},"connsDevices":11,"connsOthers":12}"""
        val s = parseUsageSample(100, 200, json)!!
        assertEquals(100L, s.rx)
        assertEquals(200L, s.tx)
        assertEquals(1763301720000000000L, s.session)
        assertEquals(listOf(1L, 2, 3, 4, 5), s.streamIn.toList())
        assertEquals(listOf(6L, 7, 8, 9, 10), s.streamOut.toList())
        assertEquals(11L, s.connsDevices)
        assertEquals(12L, s.connsOthers)
        assertNull(parseUsageSample(1, 2, """{"session":0}"""))
        assertNull(parseUsageSample(1, 2, "garbage"))
    }
}
