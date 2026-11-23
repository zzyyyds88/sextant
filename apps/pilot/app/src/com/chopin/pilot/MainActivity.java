package com.chopin.pilot;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;
import android.text.InputType;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.URL;
import java.security.cert.Certificate;
import java.security.cert.X509Certificate;

import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;

/**
 * Status page.
 *
 * <p>Everything shown here comes from one document: {@code app/status.json},
 * published by the daemon (DESIGN D23). The app has no root, starts no process
 * and scrapes no command output, so what it shows is exactly what the phone
 * believes — including the honest PENDING/DEGRADED states.
 */
public final class MainActivity extends Activity {

    private static final long REFRESH_MS = 5_000;
    /**
     * Retired request code, kept as a comment so the next reader does not
     * reintroduce the prompt: the module daemon grants POST_NOTIFICATIONS itself.
     */
    private static final String TAG = "ChopinMain";

    private final Handler handler = new Handler(Looper.getMainLooper());
    private TextView daemonView;
    private TextView appView;
    // The status page's readings, one per cell. They are fields rather than a
    // rebuilt layout because render() runs every 5 s: recreating the tree on each
    // refresh would lose scroll position and flicker.
    private TextView vDaemon;
    private TextView vA11y;
    private TextView vBg;
    private TextView vSessions;
    private TextView chipState;
    private TextView webUiUrlView;
    private TextView webUiHintView;
    private TextView unlockView;
    private TextView detailView;
    private Button webUiButton;
    private Button unlockButton;
    private Button savePatternButton;
    private Button clearPatternButton;
    private EditText patternInput;

    private PilotStatus harness = PilotStatus.unavailable("读取中…");

    @Override protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        // The unlock button only matters while the phone is locked, so this page
        // has to be reachable from the lock screen. Both are set in code as well
        // as in the manifest because MIUI honours the runtime call more reliably.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O_MR1) {
            setShowWhenLocked(true);
            setTurnScreenOn(true);
        }
        setContentView(buildUi());
        requestNotificationPermission();
        refresh();
    }

    /**
     * Android 13 hides a foreground service's notification unless granted.
     *
     * It does NOT ask. The KernelSU module runs as uid 0 at boot and grants this
     * permission itself (`keepalive.perms` includes `runtime` in the daemon's
     * config), which is the same authority as the person tapping 允许 — but without
     * a dialog. Asking here meant every launch of the app put a permission prompt in
     * front of whoever was holding the phone, including an agent that cannot answer
     * it. If the grant has not landed yet the page says so instead of blocking.
     */
    private void requestNotificationPermission() {
        if (Build.VERSION.SDK_INT < 33) return;
        if (checkSelfPermission("android.permission.POST_NOTIFICATIONS") == PackageManager.PERMISSION_GRANTED) {
            return;
        }
        Log.i(TAG, "POST_NOTIFICATIONS not granted yet; the module daemon grants it on its next pass");
    }

    @Override protected void onResume() {
        super.onResume();
        handler.post(refreshLoop);
    }

    @Override protected void onPause() {
        super.onPause();
        handler.removeCallbacks(refreshLoop);
    }

    private View buildUi() {
        ScrollView scroll = new ScrollView(this);
        // fillViewport stays OFF. With it on, ScrollView re-measures a short child with
        // an EXACTLY(viewport) spec, and a vertical LinearLayout handed an exact height
        // squeezes its children instead of growing: measured on the device, the heading
        // landed at y=1202 (mid-screen) and every row after it was 0 px tall at y=2356.
        // 42 nodes were present in the tree and the page showed one line.
        scroll.setFillViewport(false);
        scroll.setBackgroundColor(UiKit.surface(this));

        LinearLayout root = UiKit.column(this);
        int pad = UiKit.dp(this, 16);
        root.setPadding(pad, UiKit.dp(this, 8), pad, pad);
        // Measured on the device, and the reason this line exists: a ScrollView's
        // child added without LayoutParams gets FrameLayout's default, which is
        // MATCH_PARENT height. Inside a ScrollView that resolves to the viewport height,
        // so the column was measured as exactly one screen tall and every row after the
        // first was squeezed to zero height at the bottom edge — the page looked like it
        // had one heading and nothing else, while the view tree still listed all 42 nodes.
        root.setGravity(android.view.Gravity.TOP);
        root.setLayoutParams(new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        // ── header ────────────────────────────────────────────────────────────
        LinearLayout head = UiKit.row(this);
        head.addView(UiKit.heading(this, "Chopin Pilot"));
        View headGap = new View(this);
        LinearLayout.LayoutParams gapLp = new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        head.addView(headGap, gapLp);
        chipState = UiKit.chip(this, "读取中", UiKit.ink2(this), UiKit.accentWeak(this));
        head.addView(chipState);
        root.addView(head);

        TextView sub = UiKit.label(this, "这台手机的执行端");
        sub.setPadding(0, UiKit.dp(this, 2), 0, UiKit.dp(this, 12));
        root.addView(sub);

        // ── readings: four cells on one card ──────────────────────────────────
        // The same four the WebUI's rail shows. A person who has seen one screen
        // should not have to learn the other.
        LinearLayout card = UiKit.column(this);
        card.setBackground(UiKit.card(this, UiKit.surfaceCard(this), 14));
        card.setPadding(UiKit.dp(this, 14), UiKit.dp(this, 12),
                UiKit.dp(this, 14), UiKit.dp(this, 12));

        LinearLayout row1 = UiKit.row(this);
        vDaemon = UiKit.value(this, "—", UiKit.ink3(this));
        vA11y = UiKit.value(this, "—", UiKit.ink3(this));
        UiKit.addWeighted(row1, UiKit.cell(this, "守护", vDaemon), 1f);
        UiKit.addWeighted(row1, UiKit.cell(this, "无障碍", vA11y), 1f);
        card.addView(row1);

        UiKit.gap(this, card, 10);

        LinearLayout row2 = UiKit.row(this);
        vBg = UiKit.value(this, "—", UiKit.ink3(this));
        vSessions = UiKit.value(this, "—", UiKit.ink3(this));
        UiKit.addWeighted(row2, UiKit.cell(this, "后台放行", vBg), 1f);
        UiKit.addWeighted(row2, UiKit.cell(this, "会话", vSessions), 1f);
        card.addView(row2);
        root.addView(card);

        // ── actions ───────────────────────────────────────────────────────────
        LinearLayout actions = UiKit.row(this);
        actions.setPadding(0, UiKit.dp(this, 12), 0, UiKit.dp(this, 4));
        // Reachability, said out loud. These two numbers are the only way to get to
        // the daemon from another machine, and they were previously knowable only by
        // reading the source: the port appeared in three string literals and nowhere
        // on screen. A person holding the phone could not tell anyone how to connect.
        TextView accessLabel = UiKit.label(this, "电脑上打开");
        accessLabel.setPadding(0, UiKit.dp(this, 14), 0, UiKit.dp(this, 4));
        root.addView(accessLabel);

        webUiUrlView = UiKit.value(this, "https://<手机IP>:" + EnginePaths.PORT + "/ui/",
                UiKit.accent(this));
        webUiUrlView.setTextSize(13);
        webUiUrlView.setTextIsSelectable(true);
        webUiUrlView.setPadding(0, 0, 0, UiKit.dp(this, 2));
        root.addView(webUiUrlView);

        webUiHintView = UiKit.label(this, "");
        webUiHintView.setTextSize(11);
        root.addView(webUiHintView);

        webUiButton = new Button(this);
        webUiButton.setText("在本机打开");
        webUiButton.setOnClickListener(v -> {
            try {
                startActivity(new Intent(Intent.ACTION_VIEW,
                        Uri.parse(EnginePaths.LOOPBACK + EnginePaths.WEBUI_PATH)));
            } catch (Throwable t) {
                detailView.setText("无法打开 WebUI: " + t);
            }
        });
        UiKit.addWeighted(actions, webUiButton, 1f);
        unlockButton = new Button(this);
        unlockButton.setText("唤醒并解锁");
        unlockButton.setOnClickListener(v -> doUnlock());
        LinearLayout.LayoutParams unlockLp = new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        unlockLp.leftMargin = UiKit.dp(this, 8);
        actions.addView(unlockButton, unlockLp);
        root.addView(actions);

        // ── unlock credential ─────────────────────────────────────────────────
        // Kept on this page because a locked phone has no reachable remote UI —
        // that is the whole reason this activity is showWhenLocked.
        TextView unlockLabel = UiKit.label(this, "锁屏图案");
        unlockLabel.setPadding(0, UiKit.dp(this, 12), 0, UiKit.dp(this, 4));
        root.addView(unlockLabel);

        patternInput = new EditText(this);
        patternInput.setHint("1-9 的点，如 1235789");
        patternInput.setHintTextColor(UiKit.ink3(this));
        patternInput.setTextColor(UiKit.ink(this));
        patternInput.setTextSize(14);
        patternInput.setBackground(UiKit.card(this, UiKit.surfaceCard(this), 10));
        patternInput.setPadding(UiKit.dp(this, 12), UiKit.dp(this, 8),
                UiKit.dp(this, 12), UiKit.dp(this, 8));
        patternInput.setInputType(InputType.TYPE_CLASS_NUMBER
                | InputType.TYPE_NUMBER_VARIATION_PASSWORD);
        root.addView(patternInput, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        LinearLayout patternRow = UiKit.row(this);
        patternRow.setPadding(0, UiKit.dp(this, 8), 0, 0);
        savePatternButton = new Button(this);
        savePatternButton.setText("保存图案");
        savePatternButton.setOnClickListener(v -> {
            UnlockSecrets.Result r = UnlockSecrets.storePattern(patternInput.getText().toString());
            patternInput.setText("");
            unlockView.setText(r.ok ? "图案" + r.message : r.message);
            refresh();
        });
        UiKit.addWeighted(patternRow, savePatternButton, 1f);
        clearPatternButton = new Button(this);
        clearPatternButton.setText("删除图案");
        clearPatternButton.setOnClickListener(v -> {
            UnlockSecrets.Result r = UnlockSecrets.clearPattern();
            unlockView.setText(r.message);
            refresh();
        });
        LinearLayout.LayoutParams clearLp = new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, 1f);
        clearLp.leftMargin = UiKit.dp(this, 8);
        patternRow.addView(clearPatternButton, clearLp);
        root.addView(patternRow);

        unlockView = UiKit.body(this, "");
        unlockView.setTextSize(12);
        unlockView.setTextColor(UiKit.ink2(this));
        unlockView.setPadding(0, UiKit.dp(this, 6), 0, 0);
        root.addView(unlockView);

        // ── the full document, for when the four readings are not enough ──────
        TextView detailLabel = UiKit.label(this, "完整状态");
        detailLabel.setPadding(0, UiKit.dp(this, 14), 0, UiKit.dp(this, 4));
        root.addView(detailLabel);

        detailView = UiKit.value(this, "", UiKit.ink2(this));
        detailView.setTextSize(11);
        detailView.setTextIsSelectable(true);
        detailView.setPadding(UiKit.dp(this, 12), UiKit.dp(this, 10),
                UiKit.dp(this, 12), UiKit.dp(this, 10));
        detailView.setBackground(UiKit.card(this, UiKit.surfaceCard(this), 10));
        root.addView(detailView);

        TextView hint = UiKit.label(this,
                "守护进程由 chopin-core 模块以 root 启动；本软件不提权、不拉起进程、不改写无障碍设置。");
        hint.setPadding(0, UiKit.dp(this, 14), 0, UiKit.dp(this, 4));
        root.addView(hint);

        scroll.addView(root, new ScrollView.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        return scroll;
    }

    /**
     * Asks the daemon to unlock. Reports the daemon's error code verbatim: "图案
     * 错误" and "没有凭据" need completely different user action, and collapsing
     * both into "解锁失败" is how a working feature looks broken.
     */
    private void doUnlock() {
        unlockButton.setEnabled(false);
        unlockView.setText("解锁中…");
        new Thread(() -> {
            String message;
            boolean ok = false;
            try {
                JSONObject out = postLocal("/v1/device/unlock-local");
                ok = out.optBoolean("ok", false);
                message = ok ? "已解锁：" + out.optString("detail", "") : "设备未解锁";
            } catch (Throwable t) {
                message = "解锁失败：" + t.getClass().getSimpleName() + ": " + t.getMessage();
            }
            final String text = message;
            final boolean success = ok;
            handler.post(() -> {
                unlockButton.setEnabled(true);
                unlockView.setText(text);
                AppLog.get(MainActivity.this).write("unlock: " + (success ? "ok" : "failed") + " " + text);
                refresh();
            });
        }, "unlock").start();
    }

    /**
     * POSTs to the daemon over loopback with the app token.
     *
     * <p>The certificate is pinned to the fingerprint the daemon published, so a
     * local process that binds port 8443 first cannot impersonate it.
     */
    private JSONObject postLocal(String path) throws Exception {
        JSONObject api = readJSON(EnginePaths.apiFile().getAbsolutePath());
        final String pin = api.optString("cert_sha256", "");
        String token = readText(EnginePaths.appTokenFile().getAbsolutePath()).trim();
        if (token.isEmpty()) throw new IllegalStateException("app token 缺失（守护尚未发布）");
        if (pin.isEmpty()) throw new IllegalStateException("证书指纹缺失（app/api.json 不完整）");

        String url = EnginePaths.LOOPBACK + path;
        TrustManager tm = new X509TrustManager() {
            @Override public void checkClientTrusted(X509Certificate[] chain, String authType) {}
            @Override public void checkServerTrusted(X509Certificate[] chain, String authType)
                    throws java.security.cert.CertificateException {
                if (chain == null || chain.length == 0) throw new java.security.cert.CertificateException("无证书链");
                String got;
                try {
                    got = sha256Hex(chain[0].getEncoded());
                } catch (Exception e) {
                    throw new java.security.cert.CertificateException("SHA-256 不可用: " + e);
                }
                if (!got.equalsIgnoreCase(pin)) {
                    throw new java.security.cert.CertificateException("证书指纹不匹配（可能被冒充）");
                }
            }
            @Override public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
        };
        SSLContext ctx = SSLContext.getInstance("TLS");
        ctx.init(null, new TrustManager[]{tm}, new java.security.SecureRandom());
        HttpsURLConnection conn = (HttpsURLConnection) new URL(url).openConnection();
        conn.setSSLSocketFactory(ctx.getSocketFactory());
        conn.setRequestMethod("POST");
        conn.setConnectTimeout(5000);
        conn.setReadTimeout(30000);
        conn.setDoOutput(true);
        conn.setRequestProperty("Authorization", "Bearer " + token);
        conn.setRequestProperty("Content-Type", "application/json");
        conn.getOutputStream().write("{}".getBytes("UTF-8"));
        int code = conn.getResponseCode();
        String body = readStream(code >= 400 ? conn.getErrorStream() : conn.getInputStream());
        if (code >= 400) throw new IllegalStateException("HTTP " + code + ": " + body);
        return new JSONObject(body);
    }

    private static String sha256Hex(byte[] data) throws Exception {
        java.security.MessageDigest md = java.security.MessageDigest.getInstance("SHA-256");
        byte[] sum = md.digest(data);
        StringBuilder sb = new StringBuilder(sum.length * 2);
        for (byte b : sum) sb.append(String.format("%02x", b));
        return sb.toString();
    }

    private static JSONObject readJSON(String path) throws Exception {
        return new JSONObject(readText(path));
    }

    private static String readText(String path) throws Exception {
        try (InputStream in = new java.io.FileInputStream(path)) {
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            byte[] chunk = new byte[4096];
            int n;
            while ((n = in.read(chunk)) > 0) buf.write(chunk, 0, n);
            return buf.toString("UTF-8");
        }
    }

    private static String readStream(InputStream in) throws Exception {
        if (in == null) return "";
        try (InputStream s = in) {
            ByteArrayOutputStream buf = new ByteArrayOutputStream();
            byte[] chunk = new byte[4096];
            int n;
            while ((n = s.read(chunk)) > 0) buf.write(chunk, 0, n);
            return buf.toString("UTF-8");
        }
    }

    private final Runnable refreshLoop = new Runnable() {
        @Override public void run() {
            refresh();
            handler.postDelayed(this, REFRESH_MS);
        }
    };

    private void refresh() {
        new Thread(() -> {
            PilotStatus hs = PilotStatus.read();
            handler.post(() -> render(hs));
        }, "refresh").start();
    }

    private void render(PilotStatus hs) {
        harness = hs;
        if (!hs.available) {
            // The four cells say "—" rather than "不可用" four times: one word in the
            // chip explains the whole card, and repeating it per cell is noise.
            setReading(vDaemon, "—", UiKit.ink3(this));
            setReading(vA11y, "—", UiKit.ink3(this));
            setReading(vBg, "—", UiKit.ink3(this));
            setReading(vSessions, "—", UiKit.ink3(this));
            chipState.setText("●  守护未就绪");
            chipState.setTextColor(UiKit.warn(this));
            chipState.setBackground(UiKit.pill(this, UiKit.warnWeak(this)));
            detailView.setText(hs.unavailableReason);
        } else {
            boolean running = hs.daemonRunning();
            setReading(vDaemon,
                    (running ? "运行中" : hs.daemonState) + " · " + hs.daemonPid,
                    running ? UiKit.ok(this) : UiKit.danger(this));
            setReading(vA11y, hs.describeA11y(),
                    hs.describeA11y().startsWith("OK") ? UiKit.ok(this) : UiKit.warn(this));
            setReading(vBg, hs.describeBackground(),
                    hs.describeBackground().startsWith("OK") ? UiKit.ok(this) : UiKit.warn(this));
            setReading(vSessions, hs.sessionsActive + "/" + hs.sessionsMax,
                    hs.sessionsActive > 0 ? UiKit.ok(this) : UiKit.ink2(this));

            // The chip is the one line a person reads when they are not reading.
            if (!running) {
                chipState.setText("●  守护未运行");
                chipState.setTextColor(UiKit.danger(this));
                chipState.setBackground(UiKit.pill(this, UiKit.dangerWeak(this)));
            } else if (hs.sessionsActive > 0) {
                chipState.setText("●  控制中");
                chipState.setTextColor(UiKit.ok(this));
                chipState.setBackground(UiKit.pill(this, UiKit.okWeak(this)));
            } else {
                chipState.setText("●  空闲");
                chipState.setTextColor(UiKit.ink2(this));
                chipState.setBackground(UiKit.pill(this, UiKit.dark(this)
                        ? 0xFF262C37 : 0xFFECEFF6));
            }
            detailView.setText(detailText(hs));
        }
        // The URL shown has to be the one that actually works from another machine,
        // so the address comes from the device rather than from a placeholder.
        String lan = lanAddress();
        webUiUrlView.setText("https://" + (lan.isEmpty() ? "<手机IP>" : lan)
                + ":" + EnginePaths.PORT + EnginePaths.WEBUI_PATH);
        if (lan.isEmpty()) {
            webUiHintView.setText("没有连上 Wi-Fi；接上后这里会显示可用地址");
        } else {
            webUiHintView.setText("同一网络下用浏览器打开，粘贴 app token 后即可接管");
        }

        webUiButton.setEnabled(hs.available);
        unlockButton.setEnabled(hs.available && hs.daemonRunning());
        savePatternButton.setEnabled(hs.available && hs.daemonRunning());
        clearPatternButton.setEnabled(hs.available && hs.daemonRunning());

        StringBuilder u = new StringBuilder();
        u.append("凭据: 图案").append("present".equals(hs.unlockCredential) ? "已保存" : "未保存");
        u.append("  锁屏: ").append(hs.screenLocked ? "是" : "否");
        if (!hs.unlockLastAttempt.isEmpty()) {
            u.append("\n上次解锁: ").append(hs.unlockLastAttempt);
        }
        if (!"present".equals(hs.unlockCredential)) {
            u.append("\n没有凭据时自动解锁无法工作（解锁会返回 credential_missing）");
        }
        unlockView.setText(u.toString());
    }

    /**
     * The device's own LAN IPv4 address, or "" when it has none.
     *
     * Read from the interfaces rather than from a property: a phone on Wi-Fi 6 and one
     * on a hotspot report completely different properties, and the address a person
     * needs is the one their browser can reach.
     */
    private String lanAddress() {
        try {
            java.util.Enumeration<java.net.NetworkInterface> ifaces =
                    java.net.NetworkInterface.getNetworkInterfaces();
            while (ifaces.hasMoreElements()) {
                java.net.NetworkInterface nif = ifaces.nextElement();
                if (!nif.isUp() || nif.isLoopback()) continue;
                String name = nif.getName();
                // wlan0 is the one that matters here; rmnet is cellular, which a
                // laptop on the same Wi-Fi cannot reach.
                if (!name.startsWith("wlan")) continue;
                java.util.Enumeration<java.net.InetAddress> addrs = nif.getInetAddresses();
                while (addrs.hasMoreElements()) {
                    java.net.InetAddress a = addrs.nextElement();
                    if (a instanceof java.net.Inet4Address && !a.isLoopbackAddress()) {
                        return a.getHostAddress();
                    }
                }
            }
        } catch (Throwable ignored) {
            // No permission or no interface: the placeholder stays, and the hint says so.
        }
        return "";
    }

    /** Writes one reading and its state colour. */
    private void setReading(TextView v, String text, int color) {
        if (v == null) return;
        v.setText(text);
        v.setTextColor(color);
    }

    private String detailText(PilotStatus hs) {
        StringBuilder sb = new StringBuilder();
        sb.append("状态来源: ").append(PilotStatus.STATUS_PATH).append('\n');
        sb.append("App token: ").append(EnginePaths.APP_TOKEN).append('\n');
        sb.append("模块 CLI : ").append(EnginePaths.CLI).append('\n');
        sb.append("监听端口 : ").append(EnginePaths.PORT).append("（HTTPS）\n");
        sb.append("WebUI   : ").append(EnginePaths.WEBUI_PATH).append("  需要 app token\n");
        sb.append("配对码   : adb shell su -c 'chopin-core ctl pair new --agent pc'\n");
        sb.append("\n--- app log ---\n").append(tail(AppLog.get(this).file(), 4000));
        return sb.toString();
    }

    private static String tail(java.io.File f, int maxBytes) {
        try (java.io.RandomAccessFile raf = new java.io.RandomAccessFile(f, "r")) {
            long len = raf.length();
            long from = Math.max(0, len - maxBytes);
            raf.seek(from);
            byte[] buf = new byte[(int) (len - from)];
            raf.readFully(buf);
            return new String(buf, "UTF-8");
        } catch (Throwable t) {
            return "(no log)";
        }
    }
}
