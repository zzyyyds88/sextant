package com.sextant.pilot;

import android.content.Context;

import java.io.File;

/**
 * Every filesystem location the app uses, in one place.
 *
 * <p>All of them are published by the module's daemon; the app only reads. The
 * app has no root and no private privileged directory of its own any more
 * (DESIGN D21): the daemon owns {@code /data/adb/sextant-core} and hands this
 * app exactly two files through a directory it can traverse.
 */
final class EnginePaths {

    static final String PKG = "com.sextant.pilot";

    /**
     * The daemon's listening port.
     *
     * One constant, because it appeared as a bare literal in the WebUI intent, the
     * loopback POST helper and the URL shown to the user, and three copies of a port
     * number is three chances for the page to name a port nothing listens on.
     */
    static final int PORT = 8443;

    /** Loopback base URL: the phone talking to its own daemon. */
    static final String LOOPBACK = "https://127.0.0.1:" + PORT;

    /** The WebUI path. Served by the daemon, not by this app. */
    static final String WEBUI_PATH = "/ui/";

    /** Root-owned runtime tree (the daemon's). */
    static final String DATA_DIR = "/data/adb/sextant-core";

    /**
     * Where the daemon publishes the copies this app reads.
     *
     * <p>Not under {@link #DATA_DIR}: that tree is 0700 root and a normal app
     * cannot traverse it. The daemon writes into this app's own CE data dir
     * instead — root can write there, and this app reads it as itself.
     */
    static final String APP_DIR = "/data/user/0/com.sextant.pilot/files";

    /** Status copy, identical to the daemon's own status.json. */
    static final String STATUS = APP_DIR + "/status.json";

    /** Discovery file: listen URL + certificate fingerprint. */
    static final String API = APP_DIR + "/api.json";

    /** Loopback bearer token minted for this app. */
    static final String APP_TOKEN = APP_DIR + "/app-token";

    /** The CLI the app may call through su, and only for the secret subcommand. */
    static final String CLI = "/data/adb/ksu/bin/sextant-core";

    /** Unlock credential presence is visible only through the daemon's status. */
    static final String UNLOCK_SECRET = DATA_DIR + "/config/unlock-secret";

    private EnginePaths() {}

    static File appTokenFile() { return new File(APP_TOKEN); }

    static File apiFile() { return new File(API); }

    static File statusFile() { return new File(STATUS); }

    static File logFile(Context ctx) { return new File(ctx.getFilesDir(), "app.log"); }
}
