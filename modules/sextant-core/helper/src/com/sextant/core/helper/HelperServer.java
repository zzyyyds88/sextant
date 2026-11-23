package com.sextant.core.helper;

import android.net.LocalServerSocket;
import android.net.LocalSocket;
import java.io.BufferedReader;
import java.io.BufferedWriter;
import java.io.File;
import java.io.FileInputStream;
import java.io.InputStreamReader;
import java.io.OutputStreamWriter;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONObject;

/** Local abstract-socket JSON server. No TCP port, no APK, no manifest. */
final class HelperServer implements AndroidBridge.Broadcaster {
    private final AndroidBridge bridge;
    private final String token;
    private final LocalServerSocket serverSocket;
    private final List<BufferedWriter> subscribers = new ArrayList<>();

    HelperServer(AndroidBridge bridge) throws Exception {
        this.bridge = bridge;
        this.token = readToken();
        String name = System.getenv("SEXTANT_HELPER_SOCKET");
        if (name == null || name.isEmpty()) {
            name = "sextant-core-helper";
        }
        if (name.startsWith("@")) {
            name = name.substring(1);
        }
        this.serverSocket = new LocalServerSocket(name);
    }

    private static String readToken() throws Exception {
        String path = System.getenv("SEXTANT_HELPER_TOKEN_FILE");
        if (path == null || path.isEmpty()) {
            throw new IllegalStateException("SEXTANT_HELPER_TOKEN_FILE missing");
        }
        File f = new File(path);
        byte[] buf = new byte[(int) f.length()];
        FileInputStream in = new FileInputStream(f);
        int off = 0;
        while (off < buf.length) {
            int n = in.read(buf, off, buf.length - off);
            if (n < 0) break;
            off += n;
        }
        in.close();
        return new String(buf, 0, off, StandardCharsets.UTF_8).trim();
    }

    void run() throws Exception {
        System.err.println("sextant-pilot helper listening on @" + serverSocket.getLocalSocketAddress());
        while (true) {
            final LocalSocket socket = serverSocket.accept();
            Thread t = new Thread(new Runnable() {
                @Override public void run() { serve(socket); }
            }, "sextant-helper-client");
            t.setDaemon(true);
            t.start();
        }
    }

    private void serve(LocalSocket socket) {
        try {
            BufferedReader in = new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.UTF_8));
            BufferedWriter out = new BufferedWriter(new OutputStreamWriter(socket.getOutputStream(), StandardCharsets.UTF_8));
            String line = in.readLine();
            if (line == null) return;
            JSONObject req = new JSONObject(line);
            String given = req.optString("token", "");
            if (!MessageDigest.isEqual(given.getBytes(StandardCharsets.UTF_8), token.getBytes(StandardCharsets.UTF_8))) {
                write(out, error(req, "unauthorized", "bad helper token"));
                return;
            }
            String method = req.optString("method", "");
            if ("subscribe".equals(method)) {
                synchronized (subscribers) { subscribers.add(out); }
                write(out, ok(req, new JSONObject().put("subscribed", true)));
                // Keep the socket open; discard any client data until EOF.
                while (in.readLine() != null) { /* no-op */ }
                synchronized (subscribers) { subscribers.remove(out); }
                return;
            }
            JSONObject params = req.optJSONObject("params");
            if (params == null) params = new JSONObject();
            try {
                JSONObject data = bridge.dispatch(method, params);
                write(out, ok(req, data));
            } catch (Throwable t) {
                Throwable cause = t;
                if (t instanceof java.lang.reflect.InvocationTargetException && t.getCause() != null) {
                    cause = t.getCause();
                }
                cause.printStackTrace(System.err);
                write(out, error(req, cause.getClass().getSimpleName(), String.valueOf(cause.getMessage())));
            }
        } catch (Throwable ignored) {
        } finally {
            try { socket.close(); } catch (Throwable ignored) {}
        }
    }

    private static JSONObject ok(JSONObject req, JSONObject data) throws Exception {
        JSONObject r = new JSONObject();
        r.put("id", req.opt("id"));
        r.put("ok", true);
        r.put("data", data);
        return r;
    }

    private static JSONObject error(JSONObject req, String code, String message) throws Exception {
        JSONObject r = new JSONObject();
        r.put("id", req.opt("id"));
        r.put("ok", false);
        JSONObject e = new JSONObject();
        e.put("code", code);
        e.put("message", message == null ? "" : message);
        r.put("error", e);
        return r;
    }

    private static void write(BufferedWriter out, JSONObject obj) throws Exception {
        synchronized (out) {
            out.write(obj.toString());
            out.write('\n');
            out.flush();
        }
    }

    @Override
    public void broadcast(String event, JSONObject payload) {
        JSONObject frame = new JSONObject();
        try {
            frame.put("event", event);
            frame.put("payload", payload);
        } catch (Exception ignored) {}
        String line = frame.toString() + "\n";
        synchronized (subscribers) {
            List<BufferedWriter> dead = new ArrayList<>();
            for (BufferedWriter w : subscribers) {
                try {
                    synchronized (w) {
                        w.write(line);
                        w.flush();
                    }
                } catch (Throwable t) {
                    dead.add(w);
                }
            }
            subscribers.removeAll(dead);
        }
    }
}
