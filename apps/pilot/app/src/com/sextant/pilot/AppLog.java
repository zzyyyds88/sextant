package com.sextant.pilot;

import android.content.Context;

import java.io.File;
import java.io.FileOutputStream;
import java.text.SimpleDateFormat;
import java.util.Date;
import java.util.Locale;

/** Tiny rolling app log. Root engine logs separately under /data/adb. */
final class AppLog {
    private static final long MAX_BYTES = 128 * 1024;
    private static AppLog instance;
    private final File file;
    private final SimpleDateFormat fmt = new SimpleDateFormat("MM-dd HH:mm:ss", Locale.US);

    static synchronized AppLog get(Context ctx) {
        if (instance == null) instance = new AppLog(EnginePaths.logFile(ctx));
        return instance;
    }

    private AppLog(File file) { this.file = file; }

    synchronized void write(String line) {
        try {
            if (file.length() > MAX_BYTES) {
                File old = new File(file.getParentFile(), file.getName() + ".1");
                //noinspection ResultOfMethodCallIgnored
                file.renameTo(old);
            }
            try (FileOutputStream fos = new FileOutputStream(file, true)) {
                fos.write((fmt.format(new Date()) + " " + line + "\n").getBytes("UTF-8"));
            }
        } catch (Exception ignored) {
        }
    }

    File file() { return file; }
}
