package com.sextant.core.helper;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.graphics.Bitmap;
import dalvik.system.DexClassLoader;
import java.lang.reflect.Method;
import org.json.JSONArray;
import org.json.JSONObject;

/**
 * Loads the MIUI Scanner OCR engine that is already present in the ROM. The
 * engine is not copied or redistributed; classes and native libraries are
 * loaded from the installed system package at runtime.
 */
final class OcrBridge {
    private final Context ctx;
    private Class<?> engineClass;
    private Object engine;
    private Method init;
    private Method doOcr;
    private Method release;
    private Method detect;
    private Method recognize;
    private String loadError = "";
    private final StringBuilder loadLog = new StringBuilder();

    OcrBridge(Context ctx) {
        this.ctx = ctx;
        try {
            ApplicationInfo ai = ctx.getPackageManager().getApplicationInfo("com.xiaomi.scanner", 0);
            StringBuilder dex = new StringBuilder(ai.sourceDir);
            if (ai.splitSourceDirs != null) {
                for (String s : ai.splitSourceDirs) dex.append(java.io.File.pathSeparator).append(s);
            }
            String libDir = ai.nativeLibraryDir;
            loadLog.append("nativeLibraryDir=").append(libDir).append("; ");
            String cache = System.getenv("SEXTANT_OCR_CACHE");
            if (cache == null || cache.isEmpty()) {
                cache = "/data/local/tmp/sextant-pilot-ocr";
            }
            java.io.File cacheDir = new java.io.File(cache);
            if (!cacheDir.exists() && !cacheDir.mkdirs()) {
                throw new java.io.IOException("cannot create OCR cache " + cache);
            }
            java.io.File dexDir = new java.io.File(cacheDir, "dex");
            if (!dexDir.exists() && !dexDir.mkdirs()) {
                throw new java.io.IOException("cannot create OCR dex cache " + dexDir);
            }
            DexClassLoader cl = new DexClassLoader(dex.toString(),
                    dexDir.getAbsolutePath(), libDir, ClassLoader.getSystemClassLoader());
            // Native methods are resolved per class loader. Load the MIUI OCR
            // libraries through the same DexClassLoader that defines OCREngine,
            // otherwise RegisterNatives binds to the helper's class loader and
            // nativeInit is reported as missing.
            engineClass = cl.loadClass("com.xiaomi.ocr.sdk_ocr.OCREngine");
            // Runtime.load0 takes the Class whose loader owns the native
            // library. Use OCREngine so RegisterNatives binds to this loader.
            Method load0 = Runtime.class.getDeclaredMethod("load0", Class.class, String.class);
            load0.setAccessible(true);
            for (String lib : new String[]{"libc++_shared.so", "libmiocr.so", "libmiocr_tokenizer.so", "libmiocr_wrapper.so"}) {
                try {
                    load0.invoke(Runtime.getRuntime(), engineClass, libDir + "/" + lib);
                    loadLog.append(lib).append("=ok; ");
                } catch (Throwable t) {
                    Throwable cause = t.getCause() != null ? t.getCause() : t;
                    loadLog.append(lib).append("=").append(cause.getClass().getSimpleName()).append(":").append(cause.getMessage()).append("; ");
                }
            }
            // OCREngine's static initializer calls System.loadLibrary even when
            // the same .so was preloaded by absolute path. All native objects are
            // already mapped; allow init() to proceed instead of failing closed.
            try {
                java.lang.reflect.Field loaded = engineClass.getDeclaredField("sIsLoadSuccess");
                loaded.setAccessible(true);
                loaded.setBoolean(null, true);
            } catch (Throwable ignored) {}
            Method getInstance = engineClass.getMethod("getInstance");
            engine = getInstance.invoke(null);
            init = engineClass.getMethod("init", String.class, String.class);
            doOcr = engineClass.getMethod("doOCR", Bitmap.class);
            // The two-step API's array type is discovered from the detect method's
            // own return type: hard-coding the class name breaks when the ROM ships
            // the engine under different packages.
            try {
                detect = engineClass.getMethod("doOCRDetect", Bitmap.class);
                Class<?> locationArray = detect.getReturnType();
                if (locationArray.isArray()) {
                    recognize = engineClass.getMethod("doOCRRecognize", Bitmap.class, locationArray);
                }
            } catch (Throwable t) {
                loadLog.append("two-step=").append(t.getClass().getSimpleName()).append("; ");
                detect = null;
                recognize = null;
            }
            release = engineClass.getMethod("release");
        } catch (Throwable t) {
            loadError = t.getClass().getSimpleName() + ": " + t.getMessage();
        }
    }

    String loadLog() { return loadLog.toString(); }

    /** Lists the engine class's public methods so a signature mismatch is visible. */
    java.util.List<String> methodSignatures() {
        java.util.List<String> out = new java.util.ArrayList<>();
        if (engineClass == null) {
            out.add("class not loaded: " + loadError);
            return out;
        }
        try {
            for (Method m : engineClass.getMethods()) {
                StringBuilder sb = new StringBuilder(m.getReturnType().getSimpleName())
                        .append(" ").append(m.getName()).append("(");
                Class<?>[] ps = m.getParameterTypes();
                for (int i = 0; i < ps.length; i++) {
                    if (i > 0) sb.append(", ");
                    sb.append(ps[i].getSimpleName());
                }
                out.add(sb.append(")").toString());
            }
        } catch (Throwable t) {
            out.add("error: " + t);
        }
        return out;
    }

    synchronized JSONObject recognize(Bitmap bmp, int offsetX, int offsetY) throws Exception {
        if (engine == null || init == null || doOcr == null) {
            throw new IllegalStateException("ROM OCR engine unavailable: " + loadError + " / " + loadLog);
        }
        String cache = System.getenv("SEXTANT_OCR_CACHE");
        if (cache == null || cache.isEmpty()) cache = "/data/local/tmp/sextant-pilot-ocr";
        // UiAutomation.takeScreenshot() hands back a HARDWARE-backed bitmap on this
        // ROM. The OCR SDK reads pixels through getPixels(), which returns zeros for
        // a hardware bitmap — the engine then "succeeds" with zero results. Copy to
        // ARGB_8888 first; that is the difference between an empty result and no
        // result at all.
        if (bmp.getConfig() != Bitmap.Config.ARGB_8888) {
            Bitmap software = bmp.copy(Bitmap.Config.ARGB_8888, false);
            if (software != null) bmp = software;
        }
        Object initOk = init.invoke(engine, cache, null);
        Object result = null;
        String path = "doOCR";
        try {
            result = doOcr.invoke(engine, bmp);
        } catch (Throwable t) {
            loadLog.append("doOCR=").append(t.getClass().getSimpleName()).append("; ");
        }
        if (result == null && detect != null && recognize != null) {
            // The engine also exposes a detect-then-recognise pair; on this ROM
            // doOCR() alone returns null, so fall back to the two-step form rather
            // than reporting "the screen has no text".
            path = "detect+recognize";
            Object locations = detect.invoke(engine, bmp);
            int n = locations instanceof Object[] ? ((Object[]) locations).length : -1;
            loadLog.append("detect=").append(n).append("; ");
            if (locations instanceof Object[] && n > 0) {
                result = recognize.invoke(engine, bmp, locations);
            }
        }
        // init() returns a boolean; when it is false the engine runs but produces
        // nothing, which is indistinguishable from "the screen has no text".
        String initResult = String.valueOf(initOk);
        JSONArray lines = new JSONArray();
        StringBuilder all = new StringBuilder();
        String shape = "null";
        if (result != null) {
            // Report the result's real shape: an empty paragraph list and a
            // differently-shaped result look identical from the caller's side, and
            // guessing the field names is how "OCR returns nothing" stays unfixed.
            StringBuilder sb = new StringBuilder(result.getClass().getName()).append("{");
            for (java.lang.reflect.Field f : result.getClass().getFields()) {
                Object v;
                try { v = f.get(result); } catch (Throwable t) { v = "?"; }
                sb.append(f.getName()).append("=").append(v == null ? "null" : v.getClass().getSimpleName());
                if (v instanceof Object[]) sb.append("[").append(((Object[]) v).length).append("]");
                sb.append(";");
            }
            shape = sb.append("}").toString();
            Object paragraphs = field(result, "paragraphs");
            if (paragraphs instanceof Object[]) {
                for (Object p : (Object[]) paragraphs) {
                    Object ls = field(p, "lines");
                    if (!(ls instanceof Object[])) continue;
                    for (Object line : (Object[]) ls) {
                        Object tv = field(line, "line_text");
                        String text = tv == null ? "" : String.valueOf(tv);
                        Object conf = field(line, "line_confidence");
                        JSONObject o = new JSONObject();
                        o.put("text", text == null ? "" : text);
                        o.put("score", conf instanceof Number ? ((Number) conf).doubleValue() : 0.0);
                        o.put("bounds", bounds(field(line, "location"), bmp.getWidth(), bmp.getHeight(), offsetX, offsetY));
                        lines.put(o);
                        all.append(text);
                    }
                }
            }
        }
        JSONObject out = new JSONObject();
        out.put("engine", "miui-scanner");
        out.put("lines", lines);
        out.put("n", lines.length());
        out.put("text", all.toString());
        out.put("result_shape", shape);
        out.put("init", initResult);
        out.put("path", path);
        out.put("size", bmp.getWidth() + "x" + bmp.getHeight());
        return out;
    }

    private static Object field(Object o, String name) {
        if (o == null) return null;
        try {
            java.lang.reflect.Field f = o.getClass().getField(name);
            f.setAccessible(true);
            return f.get(o);
        } catch (Throwable t) {
            return null;
        }
    }

    private static JSONObject bounds(Object location, int width, int height, int offsetX, int offsetY) throws Exception {
        JSONObject b = new JSONObject();
        float[] a = (float[]) field(location, "box");
        int l = 0, t = 0, r = 0, btm = 0;
        if (a != null && a.length >= 4) {
            boolean normalized = true;
            for (float v : a) if (v > 1.5f) normalized = false;
            l = Math.round(normalized ? a[0] * width : a[0]);
            t = Math.round(normalized ? a[1] * height : a[1]);
            r = Math.round(normalized ? a[2] * width : a[2]);
            btm = Math.round(normalized ? a[3] * height : a[3]);
        }
        if (r <= l || btm <= t) {
            float[] poly = (float[]) field(location, "poly");
            if (poly != null && poly.length >= 8) {
                float minX = Float.MAX_VALUE, minY = Float.MAX_VALUE, maxX = -Float.MAX_VALUE, maxY = -Float.MAX_VALUE;
                boolean normalized = true;
                for (float v : poly) if (v > 1.5f) normalized = false;
                for (int i = 0; i + 1 < poly.length; i += 2) {
                    float px = normalized ? poly[i] * width : poly[i];
                    float py = normalized ? poly[i + 1] * height : poly[i + 1];
                    minX = Math.min(minX, px); maxX = Math.max(maxX, px);
                    minY = Math.min(minY, py); maxY = Math.max(maxY, py);
                }
                l = Math.round(minX); t = Math.round(minY);
                r = Math.round(maxX); btm = Math.round(maxY);
            }
        }
        if (r < l) { int tmp = l; l = r; r = tmp; }
        if (btm < t) { int tmp = t; t = btm; btm = tmp; }
        b.put("left", l + offsetX); b.put("top", t + offsetY);
        b.put("right", r + offsetX); b.put("bottom", btm + offsetY);
        return b;
    }
}
