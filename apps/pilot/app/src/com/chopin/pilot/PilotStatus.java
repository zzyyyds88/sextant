package com.chopin.pilot;

import org.json.JSONException;
import org.json.JSONObject;

import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.nio.charset.StandardCharsets;

/**
 * The permission state, read from the structured status file the daemon
 * publishes.
 *
 * <p>There is exactly one source: the daemon's {@code status.json}, copied into this
 * app's own files dir where a zero-root process can read it (DESIGN D20/D23). The
 * same document is served
 * by {@code GET /v1/status} to a PC, so the phone and the computer can never
 * disagree about what the device thinks its state is.
 *
 * <p>Nothing here runs a shell command and nothing parses CLI text. The previous
 * design read a root CLI's {@code key : value} text; a renamed key silently
 * blanked the page, and the status read required root the app no longer has.
 */
final class PilotStatus {

    /** Where the daemon publishes the app-readable copy (this app's own dir). */
    static final String STATUS_PATH = EnginePaths.STATUS;

    /** Schema this client understands; a mismatch is reported, not guessed. */
    static final int SCHEMA = 1;

    static final String UNKNOWN = "UNKNOWN";

    /** Set when the file is missing or unreadable, with a reason for the page. */
    String unavailableReason = "";
    boolean available;

    String daemonState = UNKNOWN;
    String daemonVersion = "";
    int daemonPid;

    String appPackage = "";
    boolean appInstalled;
    String a11yState = UNKNOWN;
    String a11yDetail = "";
    String a11yComponent = "";
    String bgAutostart = "";
    String bgControl = "";
    boolean bgDoze;
    boolean bgOk;
    String permsSummary = "";

    String unlockCredential = "";
    boolean screenLocked;
    String unlockLastAttempt = "";

    int sessionsActive;
    int sessionsMax;

    static PilotStatus unavailable(String reason) {
        PilotStatus s = new PilotStatus();
        s.available = false;
        s.unavailableReason = reason;
        return s;
    }

    /** Reads the published status; never throws. */
    static PilotStatus read() {
        File f = new File(STATUS_PATH);
        if (!f.isFile()) {
            return unavailable("守护尚未发布状态（" + STATUS_PATH + " 不存在）。"
                    + "模块未安装或守护未启动。");
        }
        byte[] raw;
        try (FileInputStream in = new FileInputStream(f)) {
            raw = new byte[(int) Math.min(f.length(), 256 * 1024)];
            int off = 0;
            while (off < raw.length) {
                int n = in.read(raw, off, raw.length - off);
                if (n <= 0) break;
                off += n;
            }
            if (off != raw.length) {
                byte[] trimmed = new byte[off];
                System.arraycopy(raw, 0, trimmed, 0, off);
                raw = trimmed;
            }
        } catch (IOException e) {
            return unavailable("读取状态失败: " + e);
        }
        String text = new String(raw, StandardCharsets.UTF_8);
        try {
            return parse(new JSONObject(text));
        } catch (JSONException e) {
            return unavailable("状态文件不是合法 JSON: " + e.getMessage());
        }
    }

    static PilotStatus parse(JSONObject o) throws JSONException {
        int schema = o.optInt("schema", -1);
        if (schema != SCHEMA) {
            return unavailable("状态 schema 不匹配: 设备 " + schema + "，本 App " + SCHEMA
                    + "。请升级 App 或模块使其一致。");
        }
        PilotStatus s = new PilotStatus();
        s.available = true;

        JSONObject daemon = o.optJSONObject("daemon");
        if (daemon != null) {
            s.daemonPid = daemon.optInt("pid", 0);
            s.daemonVersion = daemon.optString("version", "");
            s.daemonState = daemon.optString("state", UNKNOWN);
        }
        JSONObject app = o.optJSONObject("app");
        if (app != null) {
            s.appPackage = app.optString("package", "");
            s.appInstalled = app.optBoolean("installed", false);
            JSONObject a11y = app.optJSONObject("a11y");
            if (a11y != null) {
                s.a11yState = a11y.optString("state", UNKNOWN);
                s.a11yDetail = a11y.optString("detail", "");
                s.a11yComponent = a11y.optString("component", "");
            }
            JSONObject bg = app.optJSONObject("bg");
            if (bg != null) {
                s.bgAutostart = bg.optString("autostart", "");
                s.bgControl = bg.optString("bgcontrol", "");
                s.bgDoze = bg.optBoolean("doze", false);
                s.bgOk = bg.optBoolean("ok", false);
            }
            JSONObject perms = app.optJSONObject("perms");
            if (perms != null) {
                StringBuilder sb = new StringBuilder();
                java.util.Iterator<String> it = perms.keys();
                while (it.hasNext()) {
                    String k = it.next();
                    if (sb.length() > 0) sb.append(", ");
                    sb.append(k).append('=').append(perms.optString(k, UNKNOWN));
                }
                s.permsSummary = sb.toString();
            }
        }
        JSONObject unlock = o.optJSONObject("unlock");
        if (unlock != null) {
            s.unlockCredential = unlock.optString("credential", "");
            s.screenLocked = unlock.optBoolean("screen_locked", false);
            s.unlockLastAttempt = unlock.optString("last_attempt", "");
        }
        JSONObject sessions = o.optJSONObject("sessions");
        if (sessions != null) {
            s.sessionsActive = sessions.optInt("active", 0);
            s.sessionsMax = sessions.optInt("max", 0);
        }
        return s;
    }

    /** Whether the app can talk to a running daemon right now. */
    boolean daemonRunning() {
        return available && "running".equals(daemonState) && daemonPid > 0;
    }

    boolean a11yReady() {
        return "OK".equals(a11yState);
    }

    String describeA11y() {
        if (!available) return "未知（守护未发布状态）";
        return a11yState + (a11yDetail.isEmpty() ? "" : " — " + a11yDetail);
    }

    String describeBackground() {
        if (!available) return "未知（守护未发布状态）";
        return "autostart=" + bgAutostart + " bgControl=" + bgControl
                + " doze=" + (bgDoze ? "on" : "off") + (bgOk ? "（已就绪）" : "（未就绪）");
    }
}
