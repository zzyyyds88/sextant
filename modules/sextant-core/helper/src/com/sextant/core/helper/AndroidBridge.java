package com.sextant.core.helper;

import android.accessibilityservice.AccessibilityServiceInfo;
import android.app.KeyguardManager;
import android.app.UiAutomation;
import android.content.ContentResolver;
import android.content.Context;
import android.database.Cursor;
import android.database.sqlite.SQLiteDatabase;
import android.graphics.Bitmap;
import android.graphics.Rect;
import android.location.Location;
import android.location.LocationListener;
import android.location.LocationManager;
import android.net.Uri;
import android.os.Binder;
import android.os.Bundle;
import android.os.IBinder;
import android.os.Looper;
import android.os.SystemClock;
import android.util.SparseArray;
import android.telephony.SmsManager;
import android.telephony.TelephonyManager;
import android.view.KeyEvent;
import android.view.accessibility.AccessibilityEvent;
import android.view.accessibility.AccessibilityNodeInfo;
import android.view.accessibility.AccessibilityWindowInfo;
import java.io.ByteArrayOutputStream;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.InvocationHandler;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import org.json.JSONArray;
import org.json.JSONObject;

/** Root framework bridge used by the Go daemon. All methods are local-only. */
final class AndroidBridge {
    interface Broadcaster {
        void broadcast(String event, JSONObject payload);
    }

    private final Context ctx;
    private UiAutomation automation;
    private android.os.PowerManager.WakeLock wakeLock;
    private IBinder a11yOwner;
    private NotificationBridge notifications;
    private SmsBridge sms;
    private BroadcastBridge broadcasts;
    private OcrBridge ocr;
    private Broadcaster broadcaster;
    private String uiError = "";
    private String notificationError = "";
    private boolean a11yEvents = false;
    private volatile boolean a11yWindowRetrieval = false;
    /** AccessibilityManagerService FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES. */
    private static final int FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES = 1;
    private String a11yFlagError = "";

    AndroidBridge(Context ctx) {
        this.ctx = ctx;
    }

    void setBroadcaster(Broadcaster b) {
        this.broadcaster = b;
        if (notifications != null) {
            notifications.setBroadcaster(b);
        }
        if (sms != null) {
            sms.setBroadcaster(b);
        }
        if (broadcasts != null) {
            broadcasts.setBroadcaster(b);
        }
    }

    synchronized void start() {
        startAutomation();
        try {
            notifications = new NotificationBridge(ctx, broadcaster);
            notifications.start();
        } catch (Throwable t) {
            notificationError = t.getClass().getSimpleName() + ": " + t.getMessage();
        }
        try {
            sms = new SmsBridge(ctx, broadcaster);
            sms.start();
        } catch (Throwable t) {
            sms = null;
        }
        try {
            broadcasts = new BroadcastBridge(ctx, broadcaster);
        } catch (Throwable t) {
            broadcasts = null;
        }
        try {
            ocr = new OcrBridge(ctx);
        } catch (Throwable t) {
            ocr = null;
        }
    }

    private synchronized void startAutomation() {
        try {
            Class<?> connClass = Class.forName("android.app.UiAutomationConnection");
            Constructor<?> connCtor = connClass.getConstructor();
            connCtor.setAccessible(true);
            Object conn = connCtor.newInstance();
            Class<?> iface = Class.forName("android.app.IUiAutomationConnection");
            Object automationConnection = conn;
            try {
                automationConnection = Proxy.newProxyInstance(
                        AndroidBridge.class.getClassLoader(),
                        new Class<?>[]{iface},
                        new UiAutomationProxy(conn, connClass));
            } catch (Throwable ignored) {
                automationConnection = conn;
            }
            Constructor<UiAutomation> ctor = UiAutomation.class.getConstructor(Looper.class, iface);
            ctor.setAccessible(true);
            UiAutomation next = ctor.newInstance(Looper.getMainLooper(), automationConnection);
            Method connect = UiAutomation.class.getDeclaredMethod("connect");
            connect.setAccessible(true);
            connect.invoke(next);
            next.setOnAccessibilityEventListener(new UiAutomation.OnAccessibilityEventListener() {
                @Override public void onAccessibilityEvent(AccessibilityEvent event) {
                    if (!a11yEvents || broadcaster == null || event == null) return;
                    try {
                        JSONObject p = new JSONObject();
                        p.put("event_type", event.getEventType());
                        p.put("package", event.getPackageName() == null ? "" : event.getPackageName().toString());
                        p.put("class", event.getClassName() == null ? "" : event.getClassName().toString());
                        p.put("text", event.getText() == null ? "" : event.getText().toString());
                        p.put("window_id", event.getWindowId());
                        p.put("display_id", displayForWindow(event.getWindowId()));
                        p.put("timestamp", System.currentTimeMillis());
                        broadcaster.broadcast("A11Y", p);
                    } catch (Throwable ignored) {}
                }
            });
            automation = next;
            uiError = "";
        } catch (Throwable t) {
            automation = null;
            uiError = t.getClass().getSimpleName() + ": " + t.getMessage();
        }
    }

    private synchronized void stopAutomation() {
        a11yEvents = false;
        a11yWindowRetrieval = false;
        UiAutomation old = automation;
        automation = null;
        a11yOwner = null;
        if (old != null) {
            try {
                Method disconnect = UiAutomation.class.getDeclaredMethod("disconnect");
                disconnect.setAccessible(true);
                disconnect.invoke(old);
            } catch (Throwable t) {
                uiError = t.getClass().getSimpleName() + ": " + t.getMessage();
            }
        }
    }

    private final class UiAutomationProxy implements InvocationHandler {
        private final Object real;
        private final Class<?> realClass;

        UiAutomationProxy(Object real, Class<?> realClass) {
            this.real = real;
            this.realClass = realClass;
        }

        @Override public Object invoke(Object proxy, Method method, Object[] args) throws Throwable {
            if ("connect".equals(method.getName()) && args != null && args.length == 2) {
                Object client = args[0];
                try {
                    registerWithWindowRetrieval(client);
                    setPrivate(real, "mClient", client);
                    setPrivate(real, "mOwningUid", Binder.getCallingUid());
                    a11yWindowRetrieval = true;
                    return null;
                } catch (Throwable t) {
                    a11yFlagError = t.getClass().getSimpleName() + ": " + t.getMessage();
                    Method realConnect = realClass.getMethod("connect",
                            Class.forName("android.accessibilityservice.IAccessibilityServiceClient"), int.class);
                    realConnect.setAccessible(true);
                    realConnect.invoke(real, args);
                    return null;
                }
            }
            try {
                return method.invoke(real, args);
            } catch (InvocationTargetException e) {
                throw e.getCause();
            }
        }

        private void registerWithWindowRetrieval(Object client) throws Throwable {
            AccessibilityServiceInfo info = new AccessibilityServiceInfo();
            info.eventTypes = -1;
            info.feedbackType = 16;
            // Stock UiAutomationConnection omits 64 (RETRIEVE_INTERACTIVE_WINDOWS);
            // include it so getWindowsOnAllDisplays() sees virtual displays too.
            info.flags = 65554 | 64;
            Method setCapabilities = AccessibilityServiceInfo.class.getMethod("setCapabilities", int.class);
            setCapabilities.invoke(info, 15);
            a11yOwner = new Binder();
            Class<?> smClass = Class.forName("android.os.ServiceManager");
            Method getService = smClass.getDeclaredMethod("getService", String.class);
            getService.setAccessible(true);
            IBinder binder = (IBinder) getService.invoke(null, "accessibility");
            if (binder == null) throw new IllegalStateException("accessibility service missing");
            Class<?> mgrClass = Class.forName("android.view.accessibility.IAccessibilityManager");
            Class<?> mgrStub = Class.forName("android.view.accessibility.IAccessibilityManager$Stub");
            Method asInterface = mgrStub.getMethod("asInterface", IBinder.class);
            Object mgr = asInterface.invoke(null, binder);
            Class<?> clientClass = Class.forName("android.accessibilityservice.IAccessibilityServiceClient");
            Method register = mgrClass.getMethod("registerUiTestAutomationService",
                    IBinder.class, clientClass, AccessibilityServiceInfo.class, int.class);
            // flags = FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES (1).
            //
            // AccessibilityManagerService.updateServicesLocked() only binds a
            // user's a11y services while
            //     mUiAutomationService == null || suppressingAccessibilityServicesLocked() == false
            // and suppressingAccessibilityServicesLocked() is
            //     !(mUiAutomationService == null && useAccessibility()) && (mUiAutomationFlags & 1) == 0
            // so registering UiAutomation with flags = 0 unbinds EVERY third-party
            // accessibility service for as long as the automation lives. That is
            // exactly the module's a11y keep-alive target: settings still look
            // perfect (`enabled_accessibility_services` unchanged) while
            // `dumpsys accessibility` shows `Bound services:{}`, and the harness
            // watchdog rewrites the setting forever without ever recovering.
            // Measured on the MIUI 14 device: with the engine running
            // `Bound services:{}`; kill the engine (automation disconnects) and
            // all three services bind again immediately.
            //
            // Fall back to 0 only if this platform rejects the flag, so the
            // bridge still works on builds that predate it.
            try {
                register.invoke(mgr, a11yOwner, client, info, FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES);
            } catch (Throwable flagsRejected) {
                a11yFlagError = "flags1: " + flagsRejected.getClass().getSimpleName();
                register.invoke(mgr, a11yOwner, client, info, 0);
            }
        }

        private void setPrivate(Object target, String name, Object value) throws Exception {
            Field f = realClass.getDeclaredField(name);
            f.setAccessible(true);
            f.set(target, value);
        }
    }

    private AudioBridge audioBridge;

    /**
     * Where the two blocking audio calls run.
     *
     * Two threads, not one: the whole point is that a capture and a playback overlap
     * during a call. A single worker would serialise them again, just off the looper.
     */
    private final java.util.concurrent.ExecutorService audioPool =
            java.util.concurrent.Executors.newFixedThreadPool(2);

    /**
     * Builds the audio bridge on the main looper before a worker touches it.
     *
     * AudioManager constructs a handler in its own constructor and throws
     * "Can't create handler inside thread that has not called Looper.prepare()" on a
     * thread without one. Building it once here means the workers only ever use an
     * already-constructed object.
     */
    private void warmAudio() throws Exception {
        if (audioBridge != null) return;
        onMain("audio warmup", new MainCall() {
            @Override public JSONObject run() throws Exception {
                audio();
                return new JSONObject().put("warm", true);
            }
        });
    }

    /** The audio bridge, created on first use so a device without audio still boots. */
    private AudioBridge audio() throws Exception {
        if (audioBridge == null) audioBridge = new AudioBridge(SystemContext.get());
        return audioBridge;
    }

    /**
     * Runs an audio call on the MAIN looper and waits for its result.
     *
     * Measured, not guessed: calling {@code audio_state} straight from the helper's
     * socket thread failed with "Can't create handler inside thread that has not
     * called Looper.prepare()". AudioManager builds its port-event handler on first
     * use, and that handler needs a looper — the helper's per-connection threads have
     * none. Only the main looper is prepared (HelperMain prepares it), so the work is
     * handed there and the socket thread blocks on the answer.
     *
     * The timeout matters: a wedged AudioService would otherwise hold this helper
     * thread forever, and the daemon's own timeout would fire while the helper kept
     * a dead call pinned.
     */
    private JSONObject onMain(final String what, final MainCall call) throws Exception {
        return onMain(what, 20_000, call);
    }

    /**
     * The same, with an explicit deadline.
     *
     * Playback is the reason this exists: the helper blocks for the length of the
     * clip, so a fixed 20 s cap cut off a perfectly good 15 s sentence once the
     * platform's own start-up latency was added. The deadline has to follow the
     * payload, and the caller is the only one who knows how long that is.
     */
    private JSONObject onMain(final String what, final long timeoutMs, final MainCall call) throws Exception {
        final java.util.concurrent.CountDownLatch done = new java.util.concurrent.CountDownLatch(1);
        final JSONObject[] out = new JSONObject[1];
        final Throwable[] err = new Throwable[1];
        new android.os.Handler(android.os.Looper.getMainLooper()).post(new Runnable() {
            @Override public void run() {
                try {
                    out[0] = call.run();
                } catch (Throwable t) {
                    err[0] = t;
                } finally {
                    done.countDown();
                }
            }
        });
        if (!done.await(timeoutMs, java.util.concurrent.TimeUnit.MILLISECONDS)) {
            throw new IllegalStateException(what + " did not finish within "
                    + (timeoutMs / 1000) + " s on the main looper");
        }
        if (err[0] != null) {
            if (err[0] instanceof Exception) throw (Exception) err[0];
            throw new RuntimeException(err[0]);
        }
        return out[0];
    }

    /** One audio call, expressed as a closure so onMain can run it anywhere. */
    private interface MainCall {
        JSONObject run() throws Exception;
    }

    /**
     * Reads a boolean that may arrive as true/false, 1/0 or "on"/"off".
     *
     * Every parameter crosses JSON, and a caller that sends {@code "muted": "true"}
     * is not making a mistake worth failing on.
     */
    private static boolean boolParam(JSONObject params, String key, boolean fallback) {
        if (params == null || !params.has(key)) return fallback;
        Object v = params.opt(key);
        if (v instanceof Boolean) return (Boolean) v;
        String s = String.valueOf(v).trim().toLowerCase();
        if ("true".equals(s) || "1".equals(s) || "on".equals(s) || "yes".equals(s)) return true;
        if ("false".equals(s) || "0".equals(s) || "off".equals(s) || "no".equals(s)) return false;
        return fallback;
    }

    /** Methods that need a live UiAutomation session. */
    private static boolean needsAutomation(String method) {
        return "a11y_tree".equals(method) || "a11y_windows".equals(method)
                || "screenshot".equals(method) || "ocr".equals(method)
                || "observe".equals(method)
                || "ocr_png".equals(method) || "input_text".equals(method)
                || "a11y_click".equals(method) || "a11y_set_text".equals(method)
                || "a11y_scroll".equals(method)
                || "a11y_events_start".equals(method);
    }

    JSONObject dispatch(String method, JSONObject params) throws Exception {
        // Re-arm on demand. The daemon releases UiAutomation before it runs
        // `uiautomator dump` (the two sessions cannot coexist: the dump gets
        // killed), so automation is legitimately null after a handover and must
        // come back the next time a method needs it.
        if (automation == null && needsAutomation(method)) {
            startAutomation();
        }
        if ("health".equals(method)) return health();
        if ("lock_state".equals(method)) return lockState();
        if ("a11y_tree".equals(method)) return a11yTree(params);
        if ("a11y_windows".equals(method)) return a11yWindows();
        if ("ui_automation_stop".equals(method)) { stopAutomation(); return new JSONObject().put("stopped", true); }
        if ("ui_automation_start".equals(method)) { startAutomation(); return new JSONObject().put("started", automation != null); }
        if ("screenshot".equals(method)) return screenshot(params);
        if ("keep_awake".equals(method)) return keepAwake(params);
        if ("observe".equals(method)) return observe(params);
        if ("ocr".equals(method)) return ocr(params);
        if ("ocr_debug".equals(method)) return ocrDebug();
        if ("ocr_png".equals(method)) return ocrPng(params);
        if ("decode_h264".equals(method)) return decodeH264(params);
        if ("input_text".equals(method)) return inputText(params);
        if ("a11y_click".equals(method)) return a11yClick(params);
        if ("a11y_set_text".equals(method)) return a11ySetText(params);
        if ("a11y_scroll".equals(method)) return a11yScroll(params);
        if ("sms_list".equals(method)) return smsList(params);
        if ("sms_send".equals(method)) return smsSend(params);
        if ("sms_status".equals(method)) return smsStatus(params);
        if ("location".equals(method)) return location();
        // Audio: the ears and the mouth. Kept in its own bridge because every one
        // of these can fail in the "returns silence" way rather than throwing.
        // Every audio call goes to the main looper: AudioManager's port handler needs
        // one, and the socket threads have none (see onMain).
        if ("audio_state".equals(method)) {
            return onMain("audio_state", new MainCall() {
                @Override public JSONObject run() throws Exception { return audio().state(); }
            });
        }
        if ("audio_mic_mute".equals(method)) {
            final boolean muted = boolParam(params, "muted", true);
            return onMain("audio_mic_mute", new MainCall() {
                @Override public JSONObject run() throws Exception { return audio().setMicMute(muted); }
            });
        }
        if ("audio_speakerphone".equals(method)) {
            final boolean on = boolParam(params, "on", true);
            return onMain("audio_speakerphone", new MainCall() {
                @Override public JSONObject run() throws Exception { return audio().setSpeakerphone(on); }
            });
        }
        if ("audio_mode".equals(method)) {
            final String mode = params.optString("mode", "normal");
            return onMain("audio_mode", new MainCall() {
                @Override public JSONObject run() throws Exception { return audio().setMode(mode); }
            });
        }
        // Capture and playback are the two long calls, and they run on their own
        // threads rather than the main looper.
        //
        // Learned the hard way: with everything on the main looper, a 12 s clip held
        // the looper for 12 s, so a concurrent `audio listen` never got a turn and
        // the two could not overlap at all. Half-duplex is fine for a walkie-talkie
        // and useless for taking over a phone call, where hearing and speaking have
        // to happen at once. The bridge is warmed on the main looper first (its
        // AudioManager needs one to construct), then the blocking part runs on a
        // worker.
        if ("audio_listen".equals(method)) {
            final int ms = params.optInt("ms", 1000);
            final int source = params.optInt("source", 0);
            warmAudio();
            return audioPool.submit(new java.util.concurrent.Callable<JSONObject>() {
                @Override public JSONObject call() throws Exception { return audio().listen(ms, source); }
            }).get(ms + 20_000L, java.util.concurrent.TimeUnit.MILLISECONDS);
        }
        if ("audio_speak".equals(method)) {
            String b64 = params.optString("pcm_base64", "");
            if (b64.isEmpty()) return new JSONObject().put("ok", false).put("reason", "pcm_base64 is required");
            final byte[] pcm = android.util.Base64.decode(b64, android.util.Base64.DEFAULT);
            final boolean speaker = boolParam(params, "speaker", true);
            // 16 kHz mono s16le = 32000 bytes per second of sound.
            final long audioMs = (long) pcm.length * 1000L / 32000L;
            warmAudio();
            return audioPool.submit(new java.util.concurrent.Callable<JSONObject>() {
                @Override public JSONObject call() throws Exception { return audio().speak(pcm, speaker); }
            }).get(audioMs + 20_000L, java.util.concurrent.TimeUnit.MILLISECONDS);
        }
        if ("notifications".equals(method)) return notifications();
        if ("notification_dismiss".equals(method)) return notificationDismiss(params);
        if ("broadcast_watch".equals(method)) return broadcastWatch(params);
        if ("a11y_events_start".equals(method)) { a11yEvents = true; return new JSONObject().put("enabled", true); }
        if ("a11y_events_stop".equals(method)) { a11yEvents = false; return new JSONObject().put("enabled", false); }
        throw new UnsupportedOperationException("unknown method: " + method);
    }

    private JSONObject health() throws Exception {
        JSONObject o = new JSONObject();        o.put("helper", "android");
        o.put("sdk", android.os.Build.VERSION.SDK_INT);
        o.put("ui_automation", automation != null);
        o.put("ui_error", uiError);
        o.put("a11y_window_retrieval", a11yWindowRetrieval);
        o.put("a11y_flag_error", a11yFlagError);
        o.put("a11y_events", a11yEvents);
        o.put("notification_listener", notifications != null && notifications.connected());
        o.put("notification_error", notificationError);
        o.put("sms_observer", sms != null && sms.registered());
        o.put("sms_error", sms == null ? "unavailable" : sms.error());
        o.put("broadcast_watch", broadcasts == null ? "unavailable" : broadcasts.error());
        o.put("ocr", ocr != null);
        o.put("ocr_load_log", ocr == null ? "unavailable" : ocr.loadLog());
        o.put("context", ctx != null);
        return o;
    }

    private JSONObject lockState() throws Exception {
        JSONObject o = new JSONObject();
        KeyguardManager km = (KeyguardManager) ctx.getSystemService(Context.KEYGUARD_SERVICE);
        boolean locked = false;
        boolean deviceLocked = false;
        try {
            if (km != null) {
                locked = km.isKeyguardLocked();
                deviceLocked = km.isDeviceLocked();
            }
        } catch (Throwable ignored) {}
        int sim = TelephonyManager.SIM_STATE_UNKNOWN;
        try {
            TelephonyManager tm = (TelephonyManager) ctx.getSystemService(Context.TELEPHONY_SERVICE);
            if (tm != null) sim = tm.getSimState();
        } catch (Throwable ignored) {
            // app_process system context has no TelephonyServiceManager; the
            // root shell fallback reports SIM state separately.
        }
        o.put("screen_locked", locked);
        o.put("awake", true);
        o.put("device_locked", deviceLocked);
        o.put("sim_state", sim);
        o.put("sim_locked", sim == TelephonyManager.SIM_STATE_PIN_REQUIRED);
        o.put("sim_puk_required", sim == TelephonyManager.SIM_STATE_PUK_REQUIRED);
        o.put("sim_remaining_tries", -1);
        String type = "unknown";
        if (!locked) {
            type = "none";
        } else {
            JSONObject tree = a11yTree(new JSONObject().put("display_id", 0));
            String flat = tree.toString();
            if (flat.contains("lockPatternView")) type = "pattern";
            else if (sim == TelephonyManager.SIM_STATE_PIN_REQUIRED) type = "sim_pin";
            else if (km != null && km.isKeyguardSecure()) type = "pin_or_password";
            else type = "swipe";
        }
        o.put("lock_type", type);
        return o;
    }

    private UiAutomation ua() {
        if (automation == null) throw new IllegalStateException("UiAutomation unavailable: " + uiError);
        return automation;
    }

    /**
     * Holds a PARTIAL_WAKE_LOCK so a long task does not lose the screen to the
     * display timeout. This is not a full wake lock: the screen may still turn
     * off, but the CPU and the daemon's accessibility session keep running.
     * Release must be explicit; no implicit timeout is applied unless the caller
     * gives one.
     */
    private JSONObject keepAwake(JSONObject params) throws Exception {
        boolean enabled = params.optBoolean("enabled", true);
        int timeout = params.optInt("timeout_ms", 0);
        if (enabled) {
            if (wakeLock == null) {
                android.os.PowerManager pm =
                        (android.os.PowerManager) ctx.getSystemService(Context.POWER_SERVICE);
                if (pm == null) throw new IllegalStateException("PowerManager unavailable");
                wakeLock = pm.newWakeLock(android.os.PowerManager.PARTIAL_WAKE_LOCK, "sextant:keep-awake");
                wakeLock.setReferenceCounted(false);
            }
            if (timeout > 0) {
                wakeLock.acquire(timeout);
            } else {
                wakeLock.acquire();
            }
        } else if (wakeLock != null && wakeLock.isHeld()) {
            wakeLock.release();
        }
        JSONObject o = new JSONObject();
        o.put("enabled", enabled);
        o.put("held", wakeLock != null && wakeLock.isHeld());
        return o;
    }

    private JSONObject screenshot(JSONObject params) throws Exception {
        int display = params.optInt("display_id", 0);
        if (display != 0) throw new UnsupportedOperationException("helper screenshot supports display 0 only; use screencap for virtual displays");
        long t0 = SystemClock.elapsedRealtime();
        Bitmap bmp = ua().takeScreenshot();
        if (bmp == null) throw new IllegalStateException("takeScreenshot returned null");
        long capture = SystemClock.elapsedRealtime() - t0;
        bmp = cropBitmap(bmp, params);
        int maxSize = params.optInt("max_size", 1280);
        if (maxSize > 0) bmp = scaleDown(bmp, maxSize);
        JSONObject o = encodeBitmap(params, bmp);
        o.put("width", bmp.getWidth());
        o.put("height", bmp.getHeight());
        o.put("capture_ms", capture);
        return o;
    }

    /**
     * One capture, every observation the caller asked for.
     *
     * The old flow captured the screen separately for screenshot, OCR, pixels and
     * the tree. On this device a fresh capture plus PNG encoding costs more than
     * the observations themselves, so the useful unit is "one frame, several
     * answers". OCR and the tree stay at native resolution; only the encoded frame
     * honours max_size.
     */
    private JSONObject observe(JSONObject params) throws Exception {
        long t0 = SystemClock.elapsedRealtime();
        JSONObject o = new JSONObject();
        // Read the tree BEFORE taking the screenshot. takeScreenshot invalidates
        // UiAutomation's window cache on this ROM, and reading the tree afterwards
        // measured 596ms against 92ms when read first.
        long mark = SystemClock.elapsedRealtime();
        if (params.optBoolean("tree", false)) {
            o.put("tree", a11yTree(params));
            o.put("tree_ms", SystemClock.elapsedRealtime() - mark);
        }
        long captureStart = SystemClock.elapsedRealtime();
        Bitmap bmp = ua().takeScreenshot();
        if (bmp == null) throw new IllegalStateException("takeScreenshot returned null");
        long capture = SystemClock.elapsedRealtime() - captureStart;
        o.put("width", bmp.getWidth());
        o.put("height", bmp.getHeight());
        mark = SystemClock.elapsedRealtime();
        if (params.optBoolean("ocr", false)) {
            o.put("ocr", ocrBitmap(bmp, params));
            o.put("ocr_ms", SystemClock.elapsedRealtime() - mark);
            mark = SystemClock.elapsedRealtime();
        }
        if (params.optBoolean("pixels", false)) {
            o.put("pixels", pixelStats(bmp, params));
            o.put("pixels_ms", SystemClock.elapsedRealtime() - mark);
            mark = SystemClock.elapsedRealtime();
        }
        if (params.optBoolean("frame", false)) {
            Bitmap frameBmp = bmp;
            int maxSize = params.optInt("max_size", 1280);
            if (maxSize > 0) frameBmp = scaleDown(bmp, maxSize);
            JSONObject frame = encodeBitmap(params, frameBmp);
            frame.put("width", frameBmp.getWidth());
            frame.put("height", frameBmp.getHeight());
            o.put("frame", frame);
        }
        o.put("capture_ms", capture);
        o.put("total_ms", SystemClock.elapsedRealtime() - t0);
        return o;
    }

    private Bitmap cropBitmap(Bitmap bmp, JSONObject params) {
        int left = params.optInt("left", -1);
        int top = params.optInt("top", -1);
        int right = params.optInt("right", -1);
        int bottom = params.optInt("bottom", -1);
        JSONObject region = params.optJSONObject("region");
        if (region != null) {
            left = region.optInt("left", 0);
            top = region.optInt("top", 0);
            right = region.optInt("right", bmp.getWidth());
            bottom = region.optInt("bottom", bmp.getHeight());
        }
        if (right > left && bottom > top) {
            int w = Math.min(right - left, bmp.getWidth() - Math.max(0, left));
            int h = Math.min(bottom - top, bmp.getHeight() - Math.max(0, top));
            if (w > 0 && h > 0) {
                return Bitmap.createBitmap(bmp, Math.max(0, left), Math.max(0, top), w, h);
            }
        }
        return bmp;
    }

    private Bitmap scaleDown(Bitmap bmp, int maxSize) {
        int w = bmp.getWidth(), h = bmp.getHeight();
        int longest = Math.max(w, h);
        if (longest <= maxSize) return bmp;
        float ratio = (float) maxSize / (float) longest;
        int nw = Math.max(1, Math.round(w * ratio));
        int nh = Math.max(1, Math.round(h * ratio));
        Bitmap scaled = Bitmap.createScaledBitmap(bmp, nw, nh, true);
        return scaled != null ? scaled : bmp;
    }

    private JSONObject encodeBitmap(JSONObject params, Bitmap bmp) throws Exception {
        String format = params.optString("format", "jpeg").toLowerCase();
        int quality = params.optInt("quality", 80);
        if (quality < 1 || quality > 100) quality = 80;
        boolean png = "png".equals(format);
        ByteArrayOutputStream bos = new ByteArrayOutputStream();
        boolean ok = bmp.compress(png ? Bitmap.CompressFormat.PNG : Bitmap.CompressFormat.JPEG,
                png ? 100 : quality, bos);
        if (!ok) throw new IllegalStateException("bitmap compression failed");
        byte[] data = bos.toByteArray();
        JSONObject o = new JSONObject();
        o.put("format", png ? "png" : "jpeg");
        o.put("bytes", data.length);
        o.put("quality", png ? 100 : quality);
        String outputPath = params.optString("output_path", "");
        if (!outputPath.isEmpty()) {
            java.io.FileOutputStream out = new java.io.FileOutputStream(outputPath);
            try {
                out.write(data);
            } finally {
                out.close();
            }
            o.put("path", outputPath);
        } else if (png) {
            o.put("png_base64", android.util.Base64.encodeToString(data, android.util.Base64.NO_WRAP));
        } else {
            o.put("jpeg_base64", android.util.Base64.encodeToString(data, android.util.Base64.NO_WRAP));
        }
        return o;
    }

    /** Brightness and colour statistics, computed on-device from the same frame. */
    private JSONObject pixelStats(Bitmap src, JSONObject params) {
        Bitmap bmp = src;
        if (bmp.getConfig() != Bitmap.Config.ARGB_8888) {
            Bitmap copy = bmp.copy(Bitmap.Config.ARGB_8888, false);
            if (copy != null) bmp = copy;
        }
        JSONObject region = params.optJSONObject("region");
        int left = region != null ? Math.max(0, region.optInt("left", 0)) : 0;
        int top = region != null ? Math.max(0, region.optInt("top", 0)) : 0;
        int right = region != null ? Math.min(bmp.getWidth(), region.optInt("right", bmp.getWidth())) : bmp.getWidth();
        int bottom = region != null ? Math.min(bmp.getHeight(), region.optInt("bottom", bmp.getHeight())) : bmp.getHeight();
        int stride = Math.max(1, params.optInt("sample_stride", 4));
        int w = Math.max(0, right - left), h = Math.max(0, bottom - top);
        JSONObject o = new JSONObject();
        if (w == 0 || h == 0) {
            // An empty region is a caller error, not a black screen.
            try {
                o.put("pixels", 0);
                o.put("distinct_colors", 0);
                o.put("bounds", new JSONObject().put("width", 0).put("height", 0));
            } catch (org.json.JSONException e) {
                throw new IllegalStateException("pixel stats: " + e);
            }
            return o;
        }
        int[] pixels = new int[w * h];
        bmp.getPixels(pixels, 0, w, left, top, w, h);
        int total = 0, nearBlack = 0, minL = 255, maxL = 0;
        long sumLuma = 0;
        java.util.HashSet<Integer> colors = new java.util.HashSet<>();
        for (int y = 0; y < h; y += stride) {
            for (int x = 0; x < w; x += stride) {
                int p = pixels[y * w + x];
                int r8 = (p >> 16) & 0xff, g8 = (p >> 8) & 0xff, b8 = p & 0xff;
                int luma = (299 * r8 + 587 * g8 + 114 * b8) / 1000;
                sumLuma += luma;
                if (luma < 8) nearBlack++;
                if (luma < minL) minL = luma;
                if (luma > maxL) maxL = luma;
                colors.add(p & 0xffffff);
                total++;
            }
        }
        JSONObject bounds = new JSONObject();
        try {
            bounds.put("width", w);
            bounds.put("height", h);
            o.put("pixels", total);
            o.put("distinct_colors", colors.size());
            o.put("bounds", bounds);
            if (total > 0) {
                o.put("mean_luma", sumLuma / total);
                o.put("near_black", nearBlack);
                o.put("dark_ratio", (double) nearBlack / (double) total);
                o.put("min_luma", minL);
                o.put("max_luma", maxL);
                o.put("looks_black", nearBlack == total);
            }
        } catch (org.json.JSONException e) {
            throw new IllegalStateException("pixel stats: " + e);
        }
        return o;
    }

    private JSONObject a11yWindows() throws Exception {
        JSONArray displays = new JSONArray();
        SparseArray<List<AccessibilityWindowInfo>> all = ua().getWindowsOnAllDisplays();
        if (all != null) {
            for (int i = 0; i < all.size(); i++) {
                int display = all.keyAt(i);
                List<AccessibilityWindowInfo> wins = all.valueAt(i);
                JSONArray list = new JSONArray();
                if (wins != null) {
                    for (AccessibilityWindowInfo w : wins) {
                        if (w == null) continue;
                        JSONObject o = new JSONObject();
                        o.put("id", w.getId());
                        o.put("type", w.getType());
                        o.put("focused", w.isFocused());
                        o.put("root", w.getRoot() != null);
                        list.put(o);
                    }
                }
                JSONObject d = new JSONObject();
                d.put("display_id", display);
                d.put("windows", list);
                displays.put(d);
            }
        }
        JSONObject out = new JSONObject();
        out.put("displays", displays);
        return out;
    }

    private JSONObject a11yTree(JSONObject params) throws Exception {
        int display = params.optInt("display_id", 0);
        AccessibilityNodeInfo root = rootForDisplay(display);
        if (root == null) {
            if (display != 0) {
                throw new IllegalStateException("no accessibility window root for display " + display);
            }
            JSONObject empty = new JSONObject();
            empty.put("display_id", display);
            empty.put("tree", new JSONArray());
            empty.put("nodes", 0);
            empty.put("window_id", -1);
            return empty;
        }
        // Build the tree as JSON text instead of thousands of JSONObject.put calls.
        // Measured: a 563-node tree took 468ms through JSONObject and the recursion
        // below; the same tree is dominated by binder reads once the map overhead
        // is gone.
        int[] count = new int[]{0};
        boolean[] truncated = new boolean[]{false};
        StringBuilder sb = new StringBuilder(1 << 20);
        sb.append('{');
        sb.append("\"display_id\":").append(display);
        sb.append(",\"tree\":[");
        appendNode(sb, root, 0, count, truncated);
        sb.append("],\"nodes\":").append(count[0]);
        sb.append(",\"truncated\":").append(truncated[0] || count[0] >= 3000);
        sb.append(",\"window_id\":").append(root.getWindowId());
        sb.append('}');
        return new JSONObject(sb.toString());
    }

    private AccessibilityNodeInfo rootForDisplay(int display) {
        try {
            SparseArray<List<AccessibilityWindowInfo>> all = ua().getWindowsOnAllDisplays();
            if (all != null) {
                List<AccessibilityWindowInfo> wins = all.get(display);
                if (wins != null && !wins.isEmpty()) {
                    for (AccessibilityWindowInfo w : wins) {
                        if (w != null && w.isFocused()) {
                            AccessibilityNodeInfo r = w.getRoot();
                            if (r != null) return r;
                        }
                    }
                    for (AccessibilityWindowInfo w : wins) {
                        if (w == null) continue;
                        AccessibilityNodeInfo r = w.getRoot();
                        if (r != null) return r;
                    }
                }
            }
        } catch (Throwable ignored) {}
        if (display == 0) return ua().getRootInActiveWindow();
        return null;
    }

    private int displayForWindow(int windowId) {
        try {
            SparseArray<List<AccessibilityWindowInfo>> all = ua().getWindowsOnAllDisplays();
            if (all != null) {
                for (int i = 0; i < all.size(); i++) {
                    int display = all.keyAt(i);
                    List<AccessibilityWindowInfo> wins = all.valueAt(i);
                    if (wins == null) continue;
                    for (AccessibilityWindowInfo w : wins) {
                        if (w != null && w.getId() == windowId) return display;
                    }
                }
            }
        } catch (Throwable ignored) {}
        return -1;
    }

    private AccessibilityNodeInfo findEditable(AccessibilityNodeInfo node, int[] seen) {
        if (node == null || seen[0] > 3000) return null;
        seen[0]++;
        try {
            if (node.isEditable() || (node.getClassName() != null && node.getClassName().toString().contains("EditText"))) return node;
            for (int i = 0; i < node.getChildCount() && seen[0] < 3000; i++) {
                AccessibilityNodeInfo c = node.getChild(i);
                AccessibilityNodeInfo found = findEditable(c, seen);
                if (found != null) return found;
            }
        } catch (Throwable ignored) {}
        return null;
    }

    private void appendNode(StringBuilder sb, AccessibilityNodeInfo n, int depth, int[] count, boolean[] truncated) {
        count[0]++;
        sb.append('{');
        jstr(sb, "class", n.getClassName() == null ? "" : n.getClassName().toString());
        sb.append(',');
        jstr(sb, "text", n.getText() == null ? "" : n.getText().toString());
        sb.append(',');
        jstr(sb, "desc", n.getContentDescription() == null ? "" : n.getContentDescription().toString());
        sb.append(',');
        jstr(sb, "rid", n.getViewIdResourceName() == null ? "" : n.getViewIdResourceName());
        sb.append(',');
        jstr(sb, "package", n.getPackageName() == null ? "" : n.getPackageName().toString());
        sb.append(",\"clickable\":").append(n.isClickable());
        sb.append(",\"enabled\":").append(n.isEnabled());
        sb.append(",\"scrollable\":").append(n.isScrollable());
        sb.append(",\"checked\":").append(n.isChecked());
        sb.append(",\"editable\":").append(n.isEditable());
        // D29: the three states the agent needs to tell "already selected" from
        // "disabled" from "just not matched". isSelected is the one that answers
        // "did my SKU choice take effect".
        sb.append(",\"selected\":").append(n.isSelected());
        sb.append(",\"visible\":").append(n.isVisibleToUser());
        sb.append(",\"focusable\":").append(n.isFocusable());
        sb.append(",\"long_clickable\":").append(n.isLongClickable());
        sb.append(",\"depth\":").append(depth);
        sb.append(",\"window_id\":").append(n.getWindowId());
        Rect r = new Rect();
        n.getBoundsInScreen(r);
        sb.append(",\"bounds\":{\"left\":").append(r.left)
          .append(",\"top\":").append(r.top)
          .append(",\"right\":").append(r.right)
          .append(",\"bottom\":").append(r.bottom).append('}');
        if (depth < 40 && count[0] < 3000 && n.getChildCount() > 0) {
            sb.append(",\"children\":[");
            boolean firstChild = true;
            for (int i = 0; i < n.getChildCount() && count[0] < 3000; i++) {
                AccessibilityNodeInfo c = n.getChild(i);
                if (c == null) continue;
                if (!firstChild) sb.append(',');
                firstChild = false;
                appendNode(sb, c, depth + 1, count, truncated);
            }
            sb.append(']');
        } else if (n.getChildCount() > 0) {
            // Depth or node cap cut this subtree; say so instead of returning a
            // quietly incomplete tree.
            truncated[0] = true;
        }
        sb.append('}');
    }

    private static void jstr(StringBuilder sb, String key, String value) {
        sb.append('"').append(key).append("\":\"");
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                case '\b': sb.append("\\b"); break;
                case '\f': sb.append("\\f"); break;
                default:
                    if (c < 0x20) {
                        sb.append("\\u");
                        String hex = Integer.toHexString(c);
                        for (int pad = hex.length(); pad < 4; pad++) sb.append('0');
                        sb.append(hex);
                    } else {
                        sb.append(c);
                    }
            }
        }
        sb.append('"');
    }

    /** Reports the ROM OCR engine's real method signatures. */
    private JSONObject ocrDebug() throws Exception {
        JSONObject o = new JSONObject();
        if (ocr == null) {
            o.put("engine", "missing");
            return o;
        }
        o.put("engine", ocr.loadLog());
        o.put("methods", new JSONArray(ocr.methodSignatures()));
        return o;
    }

    private JSONObject ocr(JSONObject params) throws Exception {
        int display = params.optInt("display_id", 0);
        if (display != 0) throw new UnsupportedOperationException("helper OCR screenshot supports display 0 only");
        Bitmap bmp = ua().takeScreenshot();
        if (bmp == null) throw new IllegalStateException("takeScreenshot returned null");
        JSONObject o = ocrBitmap(bmp, params);
        // D28: the frame that was OCR'd is also the frame the next /v1/ui/frame
        // should serve, so encode it here instead of capturing again.
        if (params.optBoolean("return_frame", false)) {
            JSONObject frame = encodeBitmap(params, bmp);
            frame.put("width", bmp.getWidth());
            frame.put("height", bmp.getHeight());
            o.put("_frame", frame);
        }
        return o;
    }

    private JSONObject decodeH264(JSONObject params) throws Exception {
        return VideoBridge.decode(
                params.optString("config_base64", ""),
                params.optString("frame_base64", ""),
                params.optInt("width", 0),
                params.optInt("height", 0),
                params.optInt("timeout_ms", 3000));
    }

    private JSONObject ocrPng(JSONObject params) throws Exception {
        String b64 = params.optString("png_base64", "");
        if (b64.isEmpty()) throw new IllegalArgumentException("png_base64 required");
        byte[] data = android.util.Base64.decode(b64, android.util.Base64.DEFAULT);
        Bitmap bmp = android.graphics.BitmapFactory.decodeByteArray(data, 0, data.length);
        if (bmp == null) throw new IllegalArgumentException("invalid png");
        return ocrBitmap(bmp, params);
    }

    private JSONObject ocrBitmap(Bitmap bmp, JSONObject params) throws Exception {
        if (ocr == null) throw new IllegalStateException("ROM OCR engine unavailable");
        JSONObject region = params.optJSONObject("region");
        int offsetX = 0, offsetY = 0;
        if (region != null) {
            int left = Math.max(0, region.optInt("left", 0));
            int top = Math.max(0, region.optInt("top", 0));
            int right = Math.min(bmp.getWidth(), region.optInt("right", bmp.getWidth()));
            int bottom = Math.min(bmp.getHeight(), region.optInt("bottom", bmp.getHeight()));
            int w = right - left, h = bottom - top;
            if (w > 0 && h > 0) {
                bmp = Bitmap.createBitmap(bmp, left, top, w, h);
                offsetX = left;
                offsetY = top;
            }
        }
        return ocr.recognize(bmp, offsetX, offsetY);
    }

    private JSONObject inputText(JSONObject params) throws Exception {
        String text = params.optString("text", "");
        boolean clear = params.optBoolean("clear", false);
        boolean submit = params.optBoolean("submit", false);
        int display = params.optInt("display_id", 0);
        AccessibilityNodeInfo root = rootForDisplay(display);
        AccessibilityNodeInfo focus = root == null ? null : root.findFocus(AccessibilityNodeInfo.FOCUS_INPUT);
        if (focus == null && root != null) focus = findEditable(root, new int[]{0});
        if (focus == null) {
            throw new IllegalStateException("text_input_unavailable: no editable node on display " + display);
        }
        Rect focusBounds = new Rect();
        focus.getBoundsInScreen(focusBounds);
        if (!focus.isEditable() || !focus.isVisibleToUser() || focusBounds.isEmpty()) {
            throw new IllegalStateException("text_input_unavailable: focused node is not a visible editable field on display " + display);
        }
        String value = text;
        if (!clear && focus.getText() != null) value = focus.getText().toString() + text;
        Bundle args = new Bundle();
        args.putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, value);
        focus.performAction(AccessibilityNodeInfo.ACTION_FOCUS);
        boolean ok = focus.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args);
        if (!ok) {
            throw new IllegalStateException("text_input_unavailable: ACTION_SET_TEXT rejected on display " + display);
        }
        // ACTION_SET_TEXT can return true on a node that is not the real
        // composer (WeChat exposes a decoy editable). Read the field back and
        // fail over to the module IME when the value did not change.
        boolean verified = false;
        for (int i = 0; i < 4; i++) {
            SystemClock.sleep(120);
            focus.refresh();
            CharSequence after = focus.getText();
            String actual = after == null ? "" : after.toString();
            if (actual.equals(value)) {
                verified = true;
                break;
            }
        }
        if (!verified) {
            throw new IllegalStateException("text_input_unavailable: ACTION_SET_TEXT did not change the field on display " + display);
        }
        JSONObject o = new JSONObject();
        o.put("via", "a11y_set_text");
        o.put("ok", true);
        o.put("display_id", display);
        return o;
    }

    /**
     * Scrolls the list that contains the point (or the largest scrollable node).
     *
     * `input swipe 540 2000 540 800` was the only way we had, and it is wrong in the
     * same way a blind tap is wrong: it moves whatever is under that finger, which is
     * not necessarily the list — a horizontal carousel, a banner, the page behind a
     * dialog. The list itself knows how to scroll (ACTION_SCROLL_FORWARD/BACKWARD or
     * the directional actions), and a node scroll is also what a screen reader does.
     *
     * Returns {via: node|none, node_rid, node_bounds}. `none` means the caller should
     * fall back to a gesture, which it still has to do for app-drawn lists (WeChat's
     * chat view is one).
     */
    private JSONObject a11yScroll(JSONObject params) throws Exception {
        String direction = params.optString("direction", "down");
        int px = params.optInt("x", -1);
        int py = params.optInt("y", -1);
        int display = params.optInt("display_id", 0);
        AccessibilityNodeInfo root = rootForDisplay(display);
        JSONObject o = new JSONObject();
        if (root == null) { o.put("via", "none"); return o; }

        AccessibilityNodeInfo target = null;
        if (px >= 0 && py >= 0) {
            target = findScrollableAt(root, px, py, new int[]{0}, new AccessibilityNodeInfo[1], new int[]{0});
        }
        if (target == null) target = findLargestScrollable(root, new int[]{0}, new AccessibilityNodeInfo[1], new int[]{Integer.MIN_VALUE}, new int[]{0});
        if (target == null) { o.put("via", "none"); return o; }

        int action = scrollActionFor(target, direction);
        if (action == 0) { o.put("via", "none"); return o; }
        Rect r = new Rect();
        target.getBoundsInScreen(r);
        o.put("node_rid", target.getViewIdResourceName() == null ? "" : target.getViewIdResourceName());
        o.put("node_bounds", new JSONObject().put("left", r.left).put("top", r.top)
                .put("right", r.right).put("bottom", r.bottom));
        o.put("via", target.performAction(action) ? "node" : "none");
        return o;
    }

    /** ACTION_SCROLL_* for [direction], preferring the directional action the node offers. */
    private int scrollActionFor(AccessibilityNodeInfo n, String direction) {
        java.util.List<AccessibilityNodeInfo.AccessibilityAction> acts = n.getActionList();
        boolean down = false, up = false, fwd = false, back = false;
        if (acts != null) for (AccessibilityNodeInfo.AccessibilityAction a : acts) {
            if (a == null) continue;
            int id = a.getId();
            if (id == AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_DOWN.getId()) down = true;
            else if (id == AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_UP.getId()) up = true;
            else if (id == AccessibilityNodeInfo.ACTION_SCROLL_FORWARD) fwd = true;
            else if (id == AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD) back = true;
        }
        switch (direction) {
            case "up":
                if (up) return AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_UP.getId();
                return back ? AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD : 0;
            case "down":
            default:
                if (down) return AccessibilityNodeInfo.AccessibilityAction.ACTION_SCROLL_DOWN.getId();
                return fwd ? AccessibilityNodeInfo.ACTION_SCROLL_FORWARD : 0;
        }
    }

    /** The innermost scrollable node containing (x, y). */
    private AccessibilityNodeInfo findScrollableAt(AccessibilityNodeInfo n, int x, int y,
                                                   int[] best, AccessibilityNodeInfo[] holder, int[] seen) {
        if (n == null || seen[0] > 3000) return null;
        seen[0]++;
        Rect r = new Rect();
        n.getBoundsInScreen(r);
        if (!r.contains(x, y)) return null;
        int area = r.width() * r.height();
        if (n.isScrollable() && n.isVisibleToUser() && area < best[0]) {
            best[0] = area;
            holder[0] = n;
        }
        for (int i = 0; i < n.getChildCount() && seen[0] <= 3000; i++) {
            findScrollableAt(n.getChild(i), x, y, best, holder, seen);
        }
        return holder[0];
    }

    /** The scrollable node with the most screen area — the page's list. */
    private AccessibilityNodeInfo findLargestScrollable(AccessibilityNodeInfo n, int[] seen,
                                                        AccessibilityNodeInfo[] holder, int[] bestArea, int[] depth) {
        if (n == null || seen[0] > 3000 || depth[0] > 40) return null;
        seen[0]++;
        Rect r = new Rect();
        n.getBoundsInScreen(r);
        int area = r.width() * r.height();
        if (n.isScrollable() && n.isVisibleToUser() && area > bestArea[0]) {
            bestArea[0] = area;
            holder[0] = n;
        }
        for (int i = 0; i < n.getChildCount() && seen[0] <= 3000; i++) {
            depth[0]++;
            findLargestScrollable(n.getChild(i), seen, holder, bestArea, depth);
            depth[0]--;
        }
        return holder[0];
    }

    /**
     * Types into a field WITHOUT changing the device's keyboard.
     *
     * This is the DSHgo approach (Actuator.setText): find the editable node, focus it,
     * ACTION_SET_TEXT, then read the field back. It is the fast path and the polite
     * one — 1 helper call, no `ime set`, nothing of ours in front of the person's
     * keyboard. The module IME still exists for fields that refuse ACTION_SET_TEXT
     * (WeChat's self-drawn composer), but it is now the exception instead of the
     * default, and typing no longer costs the user a keyboard switch and 1.6 s.
     *
     * The editable node is located by, in order: the focused node, an explicit rid,
     * the smallest editable node containing (x, y). That last one matters because the
     * caller usually just tapped a search box and knows where it is — and "no editable
     * node on display 0" (a 404-shaped failure) was what the old path returned when
     * the field merely was not focused yet.
     */
    private JSONObject a11ySetText(JSONObject params) throws Exception {
        String text = params.optString("text", "");
        String rid = params.optString("rid", "");
        boolean clear = params.optBoolean("clear", false);
        int display = params.optInt("display_id", 0);
        int px = params.optInt("x", -1);
        int py = params.optInt("y", -1);
        AccessibilityNodeInfo root = rootForDisplay(display);
        if (root == null) throw new IllegalStateException("text_input_unavailable: no window on display " + display);

        AccessibilityNodeInfo field = root.findFocus(AccessibilityNodeInfo.FOCUS_INPUT);
        if (field != null && !field.isEditable()) field = null;
        if (field == null && !rid.isEmpty()) {
            java.util.List<AccessibilityNodeInfo> byId = root.findAccessibilityNodeInfosByViewId(rid);
            if (byId != null) for (AccessibilityNodeInfo n : byId) {
                if (n != null && n.isEditable() && n.isVisibleToUser()) { field = n; break; }
            }
        }
        if (field == null && px >= 0 && py >= 0) {
            field = findEditableAt(root, px, py, new int[]{Integer.MAX_VALUE}, new AccessibilityNodeInfo[1], new int[]{0});
        }
        if (field == null) field = findEditable(root, new int[]{0});
        if (field == null) throw new IllegalStateException("text_input_unavailable: no editable node on display " + display);
        if (!field.isEnabled() || !field.isVisibleToUser()) {
            throw new IllegalStateException("text_input_unavailable: the field is not enabled and visible");
        }
        String value = text;
        if (!clear) {
            CharSequence had = field.getText();
            if (had != null) value = had.toString() + text;
        }
        Bundle args = new Bundle();
        args.putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, value);
        if (!field.isFocused()) {
            if (!field.performAction(AccessibilityNodeInfo.ACTION_FOCUS)) {
                throw new IllegalStateException("text_input_unavailable: ACTION_FOCUS was rejected");
            }
        }
        if (!field.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)) {
            throw new IllegalStateException("text_input_unavailable: ACTION_SET_TEXT was rejected");
        }
        // Read it back: ACTION_SET_TEXT returns true on decoy nodes (WeChat exposes
        // one) and on fields the app overwrites immediately. Reporting success without
        // the readback is how "typed" turns into "nothing on screen".
        String actual = value;
        boolean verified = false;
        for (int i = 0; i < 6; i++) {
            SystemClock.sleep(90);
            field.refresh();
            CharSequence after = field.getText();
            actual = after == null ? "" : after.toString();
            if (actual.equals(value)) { verified = true; break; }
        }
        JSONObject o = new JSONObject();
        o.put("ok", true);
        o.put("via", "a11y_set_text");
        o.put("verified", verified);
        o.put("chars", value.length());
        o.put("display_id", display);
        if (!verified) o.put("actual_len", actual.length());
        return o;
    }

    /** The smallest editable node containing (x, y), or null. */
    private AccessibilityNodeInfo findEditableAt(AccessibilityNodeInfo n, int x, int y,
                                                 int[] best, AccessibilityNodeInfo[] holder, int[] seen) {
        if (n == null || seen[0] > 3000) return null;
        seen[0]++;
        Rect r = new Rect();
        n.getBoundsInScreen(r);
        if (!r.contains(x, y)) return null;
        int area = r.width() * r.height();
        if (n.isEditable() && n.isVisibleToUser() && area < best[0]) {
            best[0] = area;
            holder[0] = n;
        }
        for (int i = 0; i < n.getChildCount() && seen[0] <= 3000; i++) {
            findEditableAt(n.getChild(i), x, y, best, holder, seen);
        }
        return holder[0];
    }

    /**
     * Clicks a node, the way a person's finger would land on it.
     *
     * `input tap x y` is what this project used everywhere, and it is the wrong first
     * move: it is blind. It taps a point, so a node whose centre happens to fall on a
     * sibling (a WeChat row under a floating copy, a full-screen overlay's scrim) gets
     * the wrong thing clicked, and nothing in the reply says so — the caller sees
     * `ok` and the screen did not change.
     *
     * The order here is the one /root/chopin-research/research/dsh-go's Actuator uses, which is the correct
     * one and which we had not ported:
     *   1. the node's own ACTION_CLICK,
     *   2. the nearest clickable ANCESTOR whose bounds contain the node's centre —
     *      a finger on a label inside a row hits the row,
     *   3. only then a coordinate gesture, which the daemon performs (this process
     *      has no gesture API; UiAutomation injects events, it does not dispatch
     *      accessibility gestures).
     *
     * Returns {via: node|ancestor|none, bounds, x, y}. `none` means "use the point",
     * and the daemon does exactly that, so the behaviour degrades to what it was.
     */
    private JSONObject a11yClick(JSONObject params) throws Exception {
        String text = params.optString("text", "");
        String rid = params.optString("rid", "");
        boolean longClick = params.optBoolean("long", false);
        int display = params.optInt("display_id", 0);
        AccessibilityNodeInfo root = rootForDisplay(display);
        JSONObject o = new JSONObject();
        if (root == null) {
            o.put("via", "none");
            return o;
        }
        int px = params.optInt("x", -1);
        int py = params.optInt("y", -1);
        AccessibilityNodeInfo target = null;
        if (px >= 0 && py >= 0) {
            // Hit-test the point the caller already found, exactly as a finger would.
            //
            // This is the primary route and the reason the text lookup below is only a
            // fallback: the daemon and the helper do not always agree on WHICH node
            // "serendipity" is (the daemon's dump is built differently and can return a
            // title node while the helper's lookup finds the avatar). Clicking the point
            // the daemon chose removes that disagreement entirely.
            int[] best = new int[]{Integer.MAX_VALUE, -1};
            AccessibilityNodeInfo[] holder = new AccessibilityNodeInfo[1];
            findClickableAt(root, px, py, best, holder, new int[]{0});
            target = holder[0];
        }
        if (target == null && !rid.isEmpty()) {
            java.util.List<AccessibilityNodeInfo> byId = root.findAccessibilityNodeInfosByViewId(rid);
            if (byId != null) {
                for (AccessibilityNodeInfo n : byId) {
                    if (n != null && n.isVisibleToUser() && !isEmptyBounds(n)) { target = n; break; }
                }
            }
        }
        if (target == null && !text.isEmpty()) {
            java.util.List<AccessibilityNodeInfo> byText = root.findAccessibilityNodeInfosByText(text);
            if (byText != null) {
                for (AccessibilityNodeInfo n : byText) {
                    if (n == null || !n.isVisibleToUser() || isEmptyBounds(n)) continue;
                    CharSequence t = n.getText();
                    CharSequence d = n.getContentDescription();
                    String label = (t == null ? "" : t.toString()) + (d == null ? "" : d.toString());
                    if (!label.contains(text)) continue;
                    // An off-screen floating copy is not what the caller meant.
                    Rect r = new Rect();
                    n.getBoundsInScreen(r);
                    if (r.left < 0 || r.top < 0) continue;
                    target = n;
                    break;
                }
            }
        }
        if (target == null) {
            o.put("via", "none");
            return o;
        }
        Rect b = new Rect();
        target.getBoundsInScreen(b);
        o.put("bounds", new JSONObject().put("left", b.left).put("top", b.top)
                .put("right", b.right).put("bottom", b.bottom));
        o.put("x", b.centerX());
        o.put("y", b.centerY());
        int action = longClick ? AccessibilityNodeInfo.ACTION_LONG_CLICK : AccessibilityNodeInfo.ACTION_CLICK;
        boolean able = target.isEnabled() && (longClick ? target.isLongClickable() : target.isClickable());
        if (able && target.performAction(action)) {
            o.put("via", "node");
            return o;
        }
        // A finger on a label inside a row hits the row: walk up while the ancestor's
        // bounds still contain the point, at most 4 levels (dsh-go's limit).
        AccessibilityNodeInfo parent = target.getParent();
        int depth = 0;
        Rect pr = new Rect();
        while (parent != null && depth < 4) {
            parent.getBoundsInScreen(pr);
            boolean parentAble = parent.isEnabled()
                    && (longClick ? parent.isLongClickable() : parent.isClickable());
            if (parentAble && pr.contains(b.centerX(), b.centerY())) {
                if (parent.performAction(action)) {
                    o.put("via", "ancestor");
                    return o;
                }
                break;
            }
            parent = parent.getParent();
            depth++;
        }
        o.put("via", "none");
        return o;
    }

    /**
     * The smallest clickable node containing (x, y); null when none does.
     *
     * Smallest wins: a full-screen clickable container also contains the point, and
     * clicking it is not what a finger on a row does. The depth budget keeps a
     * pathological tree from being walked forever.
     */
    private void findClickableAt(AccessibilityNodeInfo n, int x, int y,
                                 int[] best, AccessibilityNodeInfo[] holder, int[] seen) {
        if (n == null || seen[0] > 3000) return;
        seen[0]++;
        Rect r = new Rect();
        n.getBoundsInScreen(r);
        if (!r.contains(x, y)) return;
        int area = r.width() * r.height();
        if (n.isEnabled() && n.isClickable() && n.isVisibleToUser() && area < best[0]) {
            best[0] = area;
            holder[0] = n;
        }
        for (int i = 0; i < n.getChildCount() && seen[0] <= 3000; i++) {
            findClickableAt(n.getChild(i), x, y, best, holder, seen);
        }
    }

    private boolean isEmptyBounds(AccessibilityNodeInfo n) {
        Rect r = new Rect();
        n.getBoundsInScreen(r);
        return r.width() <= 0 || r.height() <= 0;
    }

    private void injectKey(int code) {
        long now = SystemClock.uptimeMillis();
        ua().injectInputEvent(new KeyEvent(now, now, KeyEvent.ACTION_DOWN, code, 0), true);
        ua().injectInputEvent(new KeyEvent(now, now, KeyEvent.ACTION_UP, code, 0), true);
    }

    private JSONObject smsList(JSONObject params) throws Exception {
        String box = params.optString("box", "inbox");
        if (!"inbox".equals(box) && !"sent".equals(box) && !"all".equals(box)) box = "inbox";
        int limit = params.optInt("limit", 20);
        if (limit <= 0 || limit > 100) limit = 20;
        long since = params.optLong("since", 0);
        Uri uri = "all".equals(box) ? Uri.parse("content://sms") : Uri.parse("content://sms/" + box);
        String phone = params.optString("phone", "");
        StringBuilder where = new StringBuilder();
        ArrayList<String> args = new ArrayList<>();
        if (since > 0) {
            where.append("date > ?");
            args.add(String.valueOf(since));
        }
        if (!phone.isEmpty()) {
            if (where.length() > 0) where.append(" AND ");
            where.append("address = ?");
            args.add(phone);
        }
        String selection = where.length() == 0 ? null : where.toString();
        String[] selArgs = args.isEmpty() ? null : args.toArray(new String[0]);
        Cursor c = null;
        SQLiteDatabase db = null;
        try {
            c = ctx.getContentResolver().query(uri,
                    new String[]{"_id", "address", "body", "date", "read", "type"},
                    selection, selArgs, "date DESC");
        } catch (Throwable t) {
            // A system-context process has no ActivityThread app record, so
            // ActivityManager refuses provider acquisition. Root reads the same
            // telephony provider database directly as an explicit fallback.
            StringBuilder dbErr = new StringBuilder();
            String[] paths = {
                    "/data/user_de/0/com.android.providers.telephony/databases/mmssms.db",
                    "/data/data/com.android.providers.telephony/databases/mmssms.db",
                    "/data/user/0/com.android.providers.telephony/databases/mmssms.db"
            };
            for (String path : paths) {
                try {
                    db = SQLiteDatabase.openDatabase(path, null, SQLiteDatabase.OPEN_READONLY);
                    c = db.query("sms",
                            new String[]{"_id", "address", "body", "date", "read", "type"},
                            selection, selArgs, null, null, "date DESC", String.valueOf(limit));
                    break;
                } catch (Throwable dbe) {
                    dbErr.append(path).append("=").append(dbe.getClass().getSimpleName()).append(":").append(dbe.getMessage()).append("; ");
                    if (db != null) { try { db.close(); } catch (Throwable ignored2) {} db = null; }
                }
            }
            if (c == null) throw new IllegalStateException("provider=" + t.getMessage() + "; sqlite=" + dbErr);
        }
        JSONArray arr = new JSONArray();
        if (c != null) {
            try {
                while (c.moveToNext() && arr.length() < limit) {
                    JSONObject o = new JSONObject();
                    o.put("id", c.getLong(0));
                    o.put("address", c.getString(1) == null ? "" : c.getString(1));
                    o.put("body", c.getString(2) == null ? "" : c.getString(2));
                    o.put("date", c.getLong(3));
                    o.put("read", c.getInt(4) != 0);
                    o.put("type", c.getInt(5));
                    arr.put(o);
                }
            } finally {
                c.close();
                if (db != null) db.close();
            }
        }
        JSONObject o = new JSONObject();
        o.put("box", box);
        o.put("messages", arr);
        return o;
    }

    private JSONObject smsSend(JSONObject params) throws Exception {
        if (sms == null) throw new IllegalStateException("SMS bridge unavailable");
        String phone = params.optString("phone_number", params.optString("phone", ""));
        String message = params.optString("message", "");
        if (phone.isEmpty() || message.isEmpty()) throw new IllegalArgumentException("phone_number and message required");
        return sms.send(phone, message, params.optString("message_id", ""));
    }

    private JSONObject smsStatus(JSONObject params) throws Exception {
        if (sms == null) throw new IllegalStateException("SMS bridge unavailable");
        return sms.status(params.optString("message_id", ""));
    }

    private JSONObject location() throws Exception {
        LocationManager lm = (LocationManager) ctx.getSystemService(Context.LOCATION_SERVICE);
        if (lm == null) throw new IllegalStateException("LocationManager unavailable");
        Location best = null;
        for (String p : new String[]{LocationManager.GPS_PROVIDER, LocationManager.NETWORK_PROVIDER, LocationManager.PASSIVE_PROVIDER, "fused"}) {
            try {
                Location l = lm.getLastKnownLocation(p);
                if (l != null && (best == null || l.getTime() > best.getTime())) best = l;
            } catch (Throwable ignored) {}
        }
        if (best == null) {
            final CountDownLatch latch = new CountDownLatch(1);
            final Location[] out = new Location[1];
            LocationListener listener = new LocationListener() {
                @Override public void onLocationChanged(Location l) { out[0] = l; latch.countDown(); }
                @Override public void onStatusChanged(String provider, int status, Bundle extras) {}
                @Override public void onProviderEnabled(String provider) {}
                @Override public void onProviderDisabled(String provider) {}
            };
            try {
                lm.requestSingleUpdate(LocationManager.NETWORK_PROVIDER, listener, Looper.getMainLooper());
            } catch (Throwable t) {
                try { lm.requestSingleUpdate(LocationManager.GPS_PROVIDER, listener, Looper.getMainLooper()); } catch (Throwable ignored) {}
            }
            latch.await(8, TimeUnit.SECONDS);
            best = out[0];
        }
        JSONObject o = new JSONObject();
        if (best == null) {
            o.put("available", false);
            return o;
        }
        o.put("available", true);
        o.put("latitude", best.getLatitude());
        o.put("longitude", best.getLongitude());
        o.put("accuracy", best.getAccuracy());
        o.put("provider", best.getProvider());
        o.put("time", best.getTime());
        return o;
    }

    private JSONObject notifications() throws Exception {
        if (notifications == null) throw new IllegalStateException("notification listener unavailable: " + notificationError);
        return notifications.list();
    }

    private JSONObject notificationDismiss(JSONObject params) throws Exception {
        if (notifications == null) throw new IllegalStateException("notification listener unavailable: " + notificationError);
        String pkg = params.optString("package", "");
        String tag = params.optString("tag", "");
        int id = params.optInt("id", -1);
        if (pkg.isEmpty() || id < 0) throw new IllegalArgumentException("package and id required");
        notifications.dismiss(pkg, tag, id);
        return new JSONObject().put("dismissed", true);
    }

    private JSONObject broadcastWatch(JSONObject params) throws Exception {
        if (broadcasts == null) throw new IllegalStateException("broadcast bridge unavailable");
        broadcasts.watch(params.optJSONArray("actions"));
        JSONObject o = new JSONObject();
        o.put("watching", true);
        return o;
    }

    // Kept for future per-display text injection work.
    @SuppressWarnings("unused")
    private static CharSequence textOf(AccessibilityNodeInfo n) {
        return n == null ? null : n.getText();
    }
}
