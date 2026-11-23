package com.sextant.core.helper;

import android.content.Context;
import android.os.Looper;

public final class HelperMain {
    private HelperMain() {}

    public static void main(String[] args) {
        try {
            try {
                Class<?> ddm = Class.forName("android.ddm.DdmHandleAppName");
                java.lang.reflect.Method setAppName = ddm.getDeclaredMethod("setAppName", String.class, int.class);
                setAppName.setAccessible(true);
                setAppName.invoke(null, "sextant-core-helper", 0);
            } catch (Throwable ignored) {
                // pidof is an operational convenience, not a correctness gate.
            }
            try {
                Looper.prepareMainLooper();
            } catch (IllegalStateException ignored) {
                // The runtime may already have prepared it.
            }
            Context ctx = SystemContext.get();
            AndroidBridge bridge = new AndroidBridge(ctx);
            HelperServer server = new HelperServer(bridge);
            bridge.setBroadcaster(server);
            bridge.start();
            Thread serverThread = new Thread(new Runnable() {
                @Override public void run() {
                    try {
                        server.run();
                    } catch (Throwable t) {
                        t.printStackTrace(System.err);
                    }
                }
            }, "sextant-helper-server");
            serverThread.setDaemon(true);
            serverThread.start();
            // UiAutomation callbacks, Location callbacks and ContentObserver run
            // on the main looper; without this loop they are never delivered.
            Looper.loop();
        } catch (Throwable t) {
            t.printStackTrace(System.err);
            System.exit(1);
        }
    }
}
