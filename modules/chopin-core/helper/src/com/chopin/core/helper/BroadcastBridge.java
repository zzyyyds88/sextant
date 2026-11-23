package com.chopin.core.helper;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.Bundle;
import org.json.JSONArray;
import org.json.JSONObject;

/** Dynamic receiver for trigger actions. Root context only. */
final class BroadcastBridge {
    private final Context ctx;
    private AndroidBridge.Broadcaster broadcaster;
    private BroadcastReceiver receiver;
    private String actions = "";
    private String error = "";

    BroadcastBridge(Context ctx, AndroidBridge.Broadcaster broadcaster) {
        this.ctx = ctx;
        this.broadcaster = broadcaster;
    }

    void setBroadcaster(AndroidBridge.Broadcaster b) {
        this.broadcaster = b;
    }

    String error() { return error; }

    synchronized void watch(JSONArray list) throws Exception {
        if (receiver != null) {
            ctx.unregisterReceiver(receiver);
            receiver = null;
        }
        if (list == null || list.length() == 0) {
            actions = "";
            return;
        }
        IntentFilter filter = new IntentFilter();
        StringBuilder names = new StringBuilder();
        for (int i = 0; i < list.length(); i++) {
            String a = list.optString(i, "");
            if (a.isEmpty()) continue;
            filter.addAction(a);
            if (names.length() > 0) names.append(',');
            names.append(a);
        }
        receiver = new BroadcastReceiver() {
            @Override public void onReceive(Context context, Intent intent) {
                if (broadcaster == null) return;
                try {
                    JSONObject o = new JSONObject();
                    o.put("action", intent.getAction() == null ? "" : intent.getAction());
                    JSONObject extras = new JSONObject();
                    Bundle b = intent.getExtras();
                    if (b != null) {
                        for (String k : b.keySet()) {
                            Object v = b.get(k);
                            if (v != null && (v instanceof String || v instanceof Number || v instanceof Boolean)) {
                                extras.put(k, String.valueOf(v));
                            }
                        }
                    }
                    o.put("extras", extras);
                    o.put("timestamp", System.currentTimeMillis());
                    broadcaster.broadcast("BROADCAST", o);
                } catch (Throwable ignored) {}
            }
        };
        if (android.os.Build.VERSION.SDK_INT >= 33) {
            ctx.registerReceiver(receiver, filter, Context.RECEIVER_EXPORTED);
        } else {
            ctx.registerReceiver(receiver, filter);
        }
        actions = names.toString();
    }
}
