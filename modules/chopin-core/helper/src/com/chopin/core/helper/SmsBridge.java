package com.chopin.core.helper;

import android.app.Activity;
import android.app.PendingIntent;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.database.ContentObserver;
import android.database.Cursor;
import android.database.sqlite.SQLiteDatabase;
import android.net.Uri;
import android.os.Handler;
import android.os.Looper;
import android.telephony.SmsManager;
import java.util.ArrayList;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicInteger;
import org.json.JSONObject;

/** Observes the SMS provider and broadcasts newly arrived inbox messages. */
final class SmsBridge {
    private final Context ctx;
    private AndroidBridge.Broadcaster broadcaster;
    private static final String ACTION_SENT = "com.chopin.pilot.SMS_SENT";
    private long lastID = -1;
    private boolean registered;
    private boolean sentReceiverRegistered;
    private String error = "";
    private final ConcurrentHashMap<String, JSONObject> statuses = new ConcurrentHashMap<>();
    private final AtomicInteger requestSeq = new AtomicInteger(1000);

    SmsBridge(Context ctx, AndroidBridge.Broadcaster broadcaster) {
        this.ctx = ctx;
        this.broadcaster = broadcaster;
    }

    void setBroadcaster(AndroidBridge.Broadcaster b) {
        this.broadcaster = b;
    }

    boolean registered() { return registered; }
    String error() { return error; }

    void start() {
        try {
            lastID = latestID();
            ctx.getContentResolver().registerContentObserver(
                    Uri.parse("content://sms"), true, new ContentObserver(new Handler(Looper.getMainLooper())) {
                        @Override public void onChange(boolean selfChange) {
                            poll();
                        }
                    });
            registered = true;
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
        }
        // A system context has no ActivityThread app record, so dynamic receiver
        // registration may be rejected. Keep the provider observer working and
        // report submitted-only status when the receiver is unavailable.
        try {
            Thread poller = new Thread(new Runnable() {
                @Override public void run() {
                    while (true) {
                        try { Thread.sleep(2000); } catch (InterruptedException e) { return; }
                        poll();
                    }
                }
            }, "chopin-sms-poller");
            poller.setDaemon(true);
            poller.start();
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
        }
        try {
            BroadcastReceiver sent = new BroadcastReceiver() {
                @Override public void onReceive(Context context, Intent intent) {
                    String id = intent.getStringExtra("message_id");
                    if (id == null || id.isEmpty()) return;
                    int part = intent.getIntExtra("part", 0);
                    int code = getResultCode();
                    JSONObject st = statuses.get(id);
                    if (st == null) st = new JSONObject();
                    try {
                        st.put("message_id", id);
                        st.put("part", part);
                        st.put("result_code", code);
                        st.put("state", code == Activity.RESULT_OK ? "sent" : "failed");
                        st.put("updated_at", System.currentTimeMillis());
                    } catch (Throwable ignored) {}
                    statuses.put(id, st);
                }
            };
            IntentFilter filter = new IntentFilter(ACTION_SENT);
            if (android.os.Build.VERSION.SDK_INT >= 33) {
                ctx.registerReceiver(sent, filter, Context.RECEIVER_EXPORTED);
            } else {
                ctx.registerReceiver(sent, filter);
            }
            sentReceiverRegistered = true;
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
        }
    }

    private static final String[] DB_PATHS = {
            "/data/user_de/0/com.android.providers.telephony/databases/mmssms.db",
            "/data/data/com.android.providers.telephony/databases/mmssms.db",
            "/data/user/0/com.android.providers.telephony/databases/mmssms.db"
    };

    private Cursor queryDB(String selection, String[] args, String order, String limit) {
        for (String path : DB_PATHS) {
            try {
                SQLiteDatabase db = SQLiteDatabase.openDatabase(path, null, SQLiteDatabase.OPEN_READONLY);
                return db.query("sms", new String[]{"_id", "address", "body", "date"},
                        selection, args, null, null, order, limit);
            } catch (Throwable ignored) {}
        }
        return null;
    }

    private long latestID() {
        Cursor c = null;
        try {
            c = queryDB(null, null, "date DESC", "1");
            if (c != null && c.moveToFirst()) return c.getLong(0);
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
        } finally { if (c != null) c.close(); }
        return -1;
    }

    synchronized JSONObject send(String phone, String message, String messageID) throws Exception {
        if (messageID == null || messageID.isEmpty()) {
            messageID = "sms_" + UUID.randomUUID().toString().replace("-", "");
        }
        SmsManager sm = SmsManager.getDefault();
        if (sm == null) throw new IllegalStateException("SmsManager unavailable");
        ArrayList<String> parts = sm.divideMessage(message);
        if (parts == null || parts.isEmpty()) throw new IllegalArgumentException("empty message");
        int base = requestSeq.addAndGet(parts.size() + 1);
        ArrayList<PendingIntent> sentIntents = new ArrayList<>();
        int flags = PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE;
        if (sentReceiverRegistered) {
            for (int i = 0; i < parts.size(); i++) {
                Intent intent = new Intent(ACTION_SENT);
                intent.putExtra("message_id", messageID);
                intent.putExtra("part", i);
                sentIntents.add(PendingIntent.getBroadcast(ctx, base + i, intent, flags));
            }
        }
        JSONObject pending = new JSONObject();
        pending.put("message_id", messageID);
        pending.put("state", sentReceiverRegistered ? "pending" : "submitted");
        pending.put("status_supported", sentReceiverRegistered);
        pending.put("parts", parts.size());
        pending.put("created_at", System.currentTimeMillis());
        statuses.put(messageID, pending);
        if (parts.size() > 1) {
            sm.sendMultipartTextMessage(phone, null, parts,
                    sentReceiverRegistered ? sentIntents : null, null);
        } else {
            sm.sendTextMessage(phone, null, message,
                    sentReceiverRegistered ? sentIntents.get(0) : null, null);
        }
        return pending;
    }

    JSONObject status(String messageID) throws Exception {
        JSONObject st = statuses.get(messageID);
        if (st == null) {
            st = new JSONObject();
            st.put("message_id", messageID);
            st.put("state", "unknown");
        }
        return st;
    }

    private void poll() {
        Cursor c = null;
        try {
            c = queryDB("_id > ?", new String[]{String.valueOf(lastID)}, "date ASC", null);
            if (c == null) return;
            while (c.moveToNext()) {
                long id = c.getLong(0);
                if (id <= lastID) continue;
                lastID = id;
                JSONObject o = new JSONObject();
                o.put("id", id);
                o.put("address", c.getString(1) == null ? "" : c.getString(1));
                o.put("body", c.getString(2) == null ? "" : c.getString(2));
                o.put("date", c.getLong(3));
                if (broadcaster != null) broadcaster.broadcast("SMS", o);
            }
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
        } finally { if (c != null) c.close(); }
    }
}
