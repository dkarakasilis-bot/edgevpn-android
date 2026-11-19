package com.kairos.edgevpn

import androidx.test.core.app.ActivityScenario
import androidx.test.espresso.Espresso.onView
import androidx.test.espresso.action.ViewActions.click
import androidx.test.espresso.assertion.ViewAssertions.matches
import androidx.test.espresso.matcher.RootMatchers.isDialog
import androidx.test.espresso.matcher.ViewMatchers.isDisplayed
import androidx.test.espresso.matcher.ViewMatchers.withContentDescription
import androidx.test.espresso.matcher.ViewMatchers.withId
import androidx.test.espresso.matcher.ViewMatchers.withText
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.hamcrest.Matchers.allOf
import org.hamcrest.Matchers.startsWith
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class DataUsageScreenTest {

    private val context get() = InstrumentationRegistry.getInstrumentation().targetContext

    @Before
    fun startWithNothingStored() = AppSettings.startFresh(context)

    @Test
    fun opensFromTheMenu() {
        ActivityScenario.launch(MainActivity::class.java).use {
            onView(withContentDescription("More options")).perform(click())
            onView(withText("Data usage")).perform(click())

            onView(withTextBeforeHelpIcon("Keeping the VPN running")).check(matches(isDisplayed()))
        }
    }

    @Test
    fun showsNeverResetWithoutAReset() {
        ActivityScenario.launch(DataUsageActivity::class.java).use {
            onView(withId(R.id.usageSince)).check(matches(withText("Never reset")))
            onView(withId(R.id.usageTotalMobile)).check(matches(withText("0 B")))
            onView(withId(R.id.usageTotalWifi)).check(matches(withText("0 B")))
        }
    }

    @Test
    fun resetAsksAndClears() {
        val book = DataUsageBook(null, mutableMapOf(UsageNetwork.MOBILE to UsageColumn(5_000_000, 0, 0, 0, 0, 0, 0, 0)))
        AppSettings.setDataUsage(context, book.toJson())
        ActivityScenario.launch(DataUsageActivity::class.java).use {
            onView(withId(R.id.usageTotalMobile)).check(matches(withText("5.0 MB")))

            onView(withId(R.id.usageReset)).perform(click())
            onView(withText("Reset data usage?")).inRoot(isDialog()).check(matches(isDisplayed()))
            onView(withText("Reset")).inRoot(isDialog()).perform(pressButton())

            onView(withId(R.id.usageTotalMobile)).check(matches(withText("0 B")))
            onView(withId(R.id.usageSince)).check(matches(withText(startsWith("Since "))))
        }
    }

    @Test
    fun everyHelpSheetOpens() {
        val rows = listOf(
            R.id.usageTotal to "Total",
            R.id.usageVpn to "Traffic through the VPN",
            R.id.usageRunning to "Keeping the VPN running",
            R.id.usageDevices to "With your devices",
            R.id.usageDht to "DHT requests",
            R.id.usageIdentify to "Address exchange",
            R.id.usageOther to "Relays and other",
            R.id.usageSetup to "Connection setup",
            R.id.usageConnections to "New connections",
        )
        ActivityScenario.launch(DataUsageActivity::class.java).use {
            for ((id, title) in rows) {
                onView(withId(id)).perform(nestedScrollTo(), clickHelpIcon())
                onView(withId(R.id.helpTitle)).check(matches(allOf(isDisplayed(), withText(title))))
                androidx.test.espresso.Espresso.pressBack()
            }
        }
    }
}
