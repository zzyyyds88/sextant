package com.sextant.core.helper;

import android.content.Context;
import java.lang.reflect.Method;

/** Obtains a system Context from a root app_process process without an APK. */
final class SystemContext {
    private SystemContext() {}

    static Context get() throws Exception {
        Class<?> at = Class.forName("android.app.ActivityThread");
        Method systemMain = at.getDeclaredMethod("systemMain");
        systemMain.setAccessible(true);
        Object thread = systemMain.invoke(null);
        Method getSystemContext = at.getDeclaredMethod("getSystemContext");
        getSystemContext.setAccessible(true);
        Context ctx = (Context) getSystemContext.invoke(thread);
        if (ctx == null) {
            throw new IllegalStateException("getSystemContext returned null");
        }
        return ctx;
    }
}
