package com.sextant.pilot.hud;

import android.content.Context;
import org.json.JSONObject;

import java.io.File;

/** Immutable HUD provisioning file written by the sextant-pilot engine. */
final class HudConfig {
    final String serverURL;
    final String certSHA256;
    final String token;
    final String agent;

    private HudConfig(String serverURL, String certSHA256, String token, String agent) {
        this.serverURL = serverURL;
        this.certSHA256 = certSHA256;
        this.token = token;
        this.agent = agent;
    }

    static HudConfig load(Context context) throws Exception {
        File file = new File(context.getFilesDir(), "hud.json");
        if (!file.isFile()) throw new IllegalStateException("hud.json missing");
        String raw;
        try {
            raw = new String(java.nio.file.Files.readAllBytes(file.toPath()), "UTF-8");
        } catch (Throwable t) {
            raw = readLegacy(file);
        }
        JSONObject o = new JSONObject(raw);
        String url = o.optString("server_url", "");
        String cert = o.optString("server_cert_sha256", "");
        String token = o.optString("token", "");
        String agent = o.optString("agent", "hud");
        if (url.isEmpty() || cert.isEmpty() || token.isEmpty()) {
            throw new IllegalStateException("hud.json incomplete");
        }
        return new HudConfig(url, cert, token, agent);
    }

    private static String readLegacy(File file) throws Exception {
        java.io.ByteArrayOutputStream out = new java.io.ByteArrayOutputStream();
        java.io.FileInputStream in = new java.io.FileInputStream(file);
        try {
            byte[] buf = new byte[4096];
            int n;
            while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
        } finally {
            in.close();
        }
        return new String(out.toByteArray(), "UTF-8");
    }
}
