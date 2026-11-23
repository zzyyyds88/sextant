package com.sextant.pilot.hud;

import org.json.JSONObject;

import javax.net.ssl.HostnameVerifier;
import javax.net.ssl.HttpsURLConnection;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLSession;
import javax.net.ssl.SSLSocketFactory;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;
import java.io.BufferedReader;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.URL;
import java.security.MessageDigest;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;

/** Minimal SSE/HTTPS client pinned to the daemon certificate. */
final class HudClient {
    interface Listener {
        void onState(JSONObject state);
        void onDisconnected(String reason);
        void onStopped();
    }

    private final HudConfig config;
    private final Listener listener;
    private final SSLSocketFactory socketFactory;
    private final HostnameVerifier hostnameVerifier = new HostnameVerifier() {
        @Override
        public boolean verify(String hostname, SSLSession session) {
            // Certificate pinning below authenticates the loopback daemon.
            return true;
        }
    };
    private volatile boolean running;
    private volatile HttpsURLConnection current;
    private Thread thread;
    private Thread heartbeat;

    HudClient(HudConfig config, Listener listener) throws Exception {
        this.config = config;
        this.listener = listener;
        this.socketFactory = pinnedFactory(config.certSHA256);
    }

    void start() {
        if (running) return;
        running = true;
        thread = new Thread(new Runnable() {
            @Override
            public void run() {
                long backoff = 1000;
                while (running) {
                    try {
                        readEvents();
                        if (!running) return;
                    } catch (Throwable t) {
                        if (!running) return;
                        android.util.Log.w("SextantHud", "disconnected: "
                                + t.getClass().getSimpleName() + ": " + t.getMessage());
                        listener.onDisconnected(t.getClass().getSimpleName());
                    }
                    try {
                        Thread.sleep(backoff);
                    } catch (InterruptedException e) {
                        return;
                    }
                    backoff = Math.min(backoff * 2, 5000);
                }
            }
        }, "sextant-hud-client");
        thread.setDaemon(true);
        thread.start();
        heartbeat = new Thread(new Runnable() {
            @Override
            public void run() {
                while (running) {
                    try {
                        postHeartbeat();
                    } catch (Throwable t) {
                        if (running) listener.onDisconnected("heartbeat_failed");
                    }
                    try {
                        Thread.sleep(3000);
                    } catch (InterruptedException e) {
                        return;
                    }
                }
            }
        }, "sextant-hud-heartbeat");
        heartbeat.setDaemon(true);
        heartbeat.start();
    }

    void stop() {
        running = false;
        HttpsURLConnection c = current;
        if (c != null) {
            try { c.disconnect(); } catch (Throwable ignored) {}
        }
        if (thread != null) thread.interrupt();
        if (heartbeat != null) heartbeat.interrupt();
        thread = null;
        heartbeat = null;
    }

    private void postHeartbeat() throws Exception {
        HttpsURLConnection conn = null;
        try {
            URL url = new URL(config.serverURL + "/v1/hud/heartbeat");
            conn = (HttpsURLConnection) url.openConnection();
            conn.setSSLSocketFactory(socketFactory);
            conn.setHostnameVerifier(hostnameVerifier);
            conn.setConnectTimeout(5000);
            conn.setReadTimeout(5000);
            conn.setRequestMethod("POST");
            conn.setRequestProperty("Authorization", "Bearer " + config.token);
            conn.setRequestProperty("X-Sextant-Client", "hud");
            int code = conn.getResponseCode();
            if (code != 200) throw new IllegalStateException("HTTP " + code);
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    void postPause(boolean paused) {
        HttpsURLConnection conn = null;
        try {
            URL url = new URL(config.serverURL + "/v1/hud/pause");
            conn = (HttpsURLConnection) url.openConnection();
            conn.setSSLSocketFactory(socketFactory);
            conn.setHostnameVerifier(hostnameVerifier);
            conn.setConnectTimeout(5000);
            conn.setReadTimeout(5000);
            conn.setRequestMethod("POST");
            conn.setDoOutput(true);
            conn.setRequestProperty("Authorization", "Bearer " + config.token);
            conn.setRequestProperty("Content-Type", "application/json");
            conn.setRequestProperty("X-Sextant-Client", "hud");
            byte[] body = ("{\"paused\":" + paused + "}").getBytes("UTF-8");
            OutputStream out = conn.getOutputStream();
            try { out.write(body); } finally { out.close(); }
            int code = conn.getResponseCode();
            if (code != 200) throw new IllegalStateException("HTTP " + code);
        } catch (Throwable t) {
            android.util.Log.w("SextantHud", "pause failed: " + t.getClass().getSimpleName());
            listener.onDisconnected("pause_failed");
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    /**
     * Acknowledges a capture/pass-through instruction (D28).
     *
     * An ACK that does not arrive only costs the daemon its settle timeout, so a
     * failure is logged rather than treated as a disconnect.
     */
    void postAck(String requestID) {
        if (requestID == null || requestID.isEmpty()) return;
        HttpsURLConnection conn = null;
        try {
            URL url = new URL(config.serverURL + "/v1/hud/ack");
            // Loopback must bypass any system Wi-Fi proxy: routing an ACK through
            // one adds a connect/handshake delay that is larger than the wait it
            // is meant to remove.
            conn = (HttpsURLConnection) url.openConnection(java.net.Proxy.NO_PROXY);
            conn.setSSLSocketFactory(socketFactory);
            conn.setHostnameVerifier(hostnameVerifier);
            conn.setConnectTimeout(1000);
            conn.setReadTimeout(1000);
            conn.setRequestMethod("POST");
            conn.setDoOutput(true);
            conn.setRequestProperty("Authorization", "Bearer " + config.token);
            conn.setRequestProperty("Content-Type", "application/json");
            conn.setRequestProperty("X-Sextant-Client", "hud");
            byte[] body = ("{\"request_id\":\"" + requestID.replace("\"", "") + "\"}").getBytes("UTF-8");
            OutputStream out = conn.getOutputStream();
            try { out.write(body); } finally { out.close(); }
            int code = conn.getResponseCode();
            // Drain the response so the connection returns to the keep-alive pool.
            // Calling disconnect() here forced a fresh TLS handshake on every ACK,
            // which measured ~180ms; on ACK-driven input that is the whole delay.
            InputStream in = code >= 400 ? conn.getErrorStream() : conn.getInputStream();
            if (in != null) {
                byte[] buf = new byte[1024];
                try { while (in.read(buf) != -1) { /* drain */ } } finally { in.close(); }
            }
            if (code != 200) throw new IllegalStateException("HTTP " + code);
        } catch (Throwable t) {
            android.util.Log.w("SextantHud", "ack failed: " + t.getClass().getSimpleName());
            if (conn != null) conn.disconnect();
        }
    }

    void postStop() {
        HttpsURLConnection conn = null;
        try {
            URL url = new URL(config.serverURL + "/v1/hud/stop");
            conn = (HttpsURLConnection) url.openConnection();
            conn.setSSLSocketFactory(socketFactory);
            conn.setHostnameVerifier(hostnameVerifier);
            conn.setConnectTimeout(5000);
            conn.setReadTimeout(5000);
            conn.setRequestMethod("POST");
            conn.setDoOutput(true);
            conn.setRequestProperty("Authorization", "Bearer " + config.token);
            conn.setRequestProperty("Content-Type", "application/json");
            conn.setRequestProperty("X-Sextant-Client", "hud");
            byte[] body = "{\"reason\":\"user_stop\"}".getBytes("UTF-8");
            OutputStream out = conn.getOutputStream();
            try { out.write(body); } finally { out.close(); }
            int code = conn.getResponseCode();
            if (code != 200) throw new IllegalStateException("HTTP " + code);
            listener.onStopped();
        } catch (Throwable t) {
            listener.onDisconnected("stop_failed");
        } finally {
            if (conn != null) conn.disconnect();
        }
    }

    private void readEvents() throws Exception {
        URL url = new URL(config.serverURL + "/v1/hud/events");
        HttpsURLConnection conn = (HttpsURLConnection) url.openConnection();
        current = conn;
        try {
            conn.setSSLSocketFactory(socketFactory);
            conn.setHostnameVerifier(hostnameVerifier);
            conn.setConnectTimeout(5000);
            conn.setReadTimeout(0);
            conn.setRequestProperty("Authorization", "Bearer " + config.token);
            conn.setRequestProperty("Accept", "text/event-stream");
            conn.setRequestProperty("X-Sextant-Client", "hud");
            int code = conn.getResponseCode();
            if (code != 200) throw new IllegalStateException("HTTP " + code);
            InputStream in = conn.getInputStream();
            BufferedReader reader = new BufferedReader(new InputStreamReader(in, "UTF-8"));
            StringBuilder data = new StringBuilder();
            String line;
            while (running && (line = reader.readLine()) != null) {
                if (line.isEmpty()) {
                    if (data.length() > 0) {
                        dispatch(data.toString());
                        data.setLength(0);
                    }
                    continue;
                }
                if (line.startsWith(":")) continue;
                if (line.startsWith("data:")) {
                    if (data.length() > 0) data.append('\n');
                    data.append(line.substring(5).trim());
                }
            }
            if (running) throw new IllegalStateException("stream ended");
        } finally {
            current = null;
            try { conn.disconnect(); } catch (Throwable ignored) {}
        }
    }

    private void dispatch(String raw) {
        try {
            listener.onState(new JSONObject(raw));
        } catch (Throwable ignored) {
            // Ignore malformed events; the next state replaces this one.
        }
    }

    private static SSLSocketFactory pinnedFactory(final String expectedHex) throws Exception {
        final byte[] expected = hex(expectedHex);
        TrustManager[] managers = new TrustManager[]{new X509TrustManager() {
            @Override
            public void checkClientTrusted(X509Certificate[] chain, String authType) throws CertificateException {
                throw new CertificateException("client certificates are not used");
            }

            @Override
            public void checkServerTrusted(X509Certificate[] chain, String authType) throws CertificateException {
                if (chain == null || chain.length == 0) throw new CertificateException("empty chain");
                try {
                    byte[] actual = MessageDigest.getInstance("SHA-256").digest(chain[0].getEncoded());
                    if (!MessageDigest.isEqual(expected, actual)) {
                        throw new CertificateException("certificate pin mismatch");
                    }
                } catch (CertificateException e) {
                    throw e;
                } catch (Exception e) {
                    throw new CertificateException("certificate pin check failed");
                }
            }

            @Override
            public X509Certificate[] getAcceptedIssuers() {
                return new X509Certificate[0];
            }
        }};
        SSLContext context = SSLContext.getInstance("TLS");
        context.init(null, managers, new java.security.SecureRandom());
        return context.getSocketFactory();
    }

    private static byte[] hex(String value) {
        String s = value.trim();
        if ((s.length() & 1) != 0) throw new IllegalArgumentException("bad certificate pin");
        byte[] out = new byte[s.length() / 2];
        for (int i = 0; i < out.length; i++) {
            out[i] = (byte) Integer.parseInt(s.substring(i * 2, i * 2 + 2), 16);
        }
        return out;
    }
}
