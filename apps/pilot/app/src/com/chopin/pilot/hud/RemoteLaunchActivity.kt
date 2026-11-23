package com.chopin.pilot.hud

import android.app.Activity
import android.os.Bundle

/** No-UI launcher: clears the package's stopped state so MIUI binds the accessibility service. */
class RemoteLaunchActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        finish()
    }
}
