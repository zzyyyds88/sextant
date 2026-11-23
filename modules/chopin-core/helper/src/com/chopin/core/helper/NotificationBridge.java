package com.chopin.core.helper;

import android.content.ComponentName;
import android.content.Context;
import android.os.Binder;
import android.os.IBinder;
import android.os.Parcel;
import android.service.notification.StatusBarNotification;
import java.lang.reflect.Method;
import java.util.ArrayDeque;
import java.util.Deque;
import java.util.Iterator;
import org.json.JSONArray;
import org.json.JSONObject;

/** Root INotificationListener registration without an installed APK. */
final class NotificationBridge {
    private static final int MAX = 500;
    private final Context ctx;
    private AndroidBridge.Broadcaster broadcaster;
    private final Deque<JSONObject> ring = new ArrayDeque<>();
    private volatile boolean connected = false;
    private String error = "";
    private Binder listener;
    private IBinder service;

    NotificationBridge(Context ctx, AndroidBridge.Broadcaster broadcaster) {
        this.ctx = ctx;
        this.broadcaster = broadcaster;
    }

    void setBroadcaster(AndroidBridge.Broadcaster broadcaster) {
        this.broadcaster = broadcaster;
    }

    boolean connected() { return connected; }

    void start() throws Exception {
        Class<?> sm = Class.forName("android.os.ServiceManager");
        Method getService = sm.getDeclaredMethod("getService", String.class);
        getService.setAccessible(true);
        IBinder nm = (IBinder) getService.invoke(null, "notification");
        if (nm == null) throw new IllegalStateException("notification service missing");
        service = nm;
        listener = new Listener();
        Parcel data = Parcel.obtain();
        Parcel reply = Parcel.obtain();
        try {
            data.writeInterfaceToken("android.app.INotificationManager");
            data.writeStrongBinder(listener);
            data.writeTypedObject(new ComponentName("com.android.shell", "ChopinPrivListener"), 0);
            data.writeInt(0);
            nm.transact(69, data, reply, 0);
            reply.readException();
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
            throw t;
        } finally {
            data.recycle();
            reply.recycle();
        }
    }

    void dismiss(String pkg, String tag, int id) throws Exception {
        if (service == null) throw new IllegalStateException("notification service unavailable");
        Parcel data = Parcel.obtain();
        Parcel reply = Parcel.obtain();
        try {
            data.writeInterfaceToken("android.app.INotificationManager");
            data.writeString(pkg);
            data.writeString("com.android.shell");
            data.writeString(tag == null || tag.isEmpty() ? null : tag);
            data.writeInt(id);
            data.writeInt(0);
            service.transact(8, data, reply, 0);
            reply.readException();
        } finally {
            data.recycle();
            reply.recycle();
        }
    }

    JSONObject list() throws Exception {
        JSONArray arr = new JSONArray();
        long cutoff = System.currentTimeMillis() - 24L * 60 * 60 * 1000;
        synchronized (ring) {
            Iterator<JSONObject> it = ring.iterator();
            while (it.hasNext()) {
                JSONObject item = it.next();
                long posted = item.optLong("post_time", 0);
                if (posted > 0 && posted < cutoff) continue;
                arr.put(item);
            }
        }
        JSONObject o = new JSONObject();
        o.put("connected", connected);
        o.put("error", error);
        o.put("items", arr);
        return o;
    }

    private void add(StatusBarNotification sbn, boolean removed) {
        if (sbn == null) return;
        JSONObject o = new JSONObject();
        try {
            o.put("package", sbn.getPackageName());
            o.put("id", sbn.getId());
            o.put("tag", sbn.getTag() == null ? "" : sbn.getTag());
            o.put("key", sbn.getKey());
            o.put("post_time", sbn.getPostTime());
            o.put("is_ongoing", sbn.isOngoing());
            o.put("removed", removed);
            String title = "", text = "";
            android.app.Notification n = sbn.getNotification();
            if (n != null && n.extras != null) {
                CharSequence t = n.extras.getCharSequence(android.app.Notification.EXTRA_TITLE);
                CharSequence x = n.extras.getCharSequence(android.app.Notification.EXTRA_TEXT);
                if (t != null) title = t.toString();
                if (x != null) text = x.toString();
            }
            o.put("title", title);
            o.put("text", text);
        } catch (Throwable ignored) {}
        synchronized (ring) {
            ring.addFirst(o);
            while (ring.size() > MAX) ring.removeLast();
        }
        if (broadcaster != null) {
            broadcaster.broadcast("NOTIFICATION", o);
        }
    }

    private StatusBarNotification readHolder(Parcel data) {
        try {
            IBinder holder = data.readStrongBinder();
            if (holder == null) return null;
            Parcel p = Parcel.obtain();
            Parcel r = Parcel.obtain();
            try {
                p.writeInterfaceToken("android.service.notification.IStatusBarNotificationHolder");
                holder.transact(1, p, r, 0);
                r.readException();
                return (StatusBarNotification) r.readTypedObject(StatusBarNotification.CREATOR);
            } finally {
                p.recycle();
                r.recycle();
            }
        } catch (Throwable t) {
            error = t.getClass().getSimpleName() + ": " + t.getMessage();
            return null;
        }
    }

    private final class Listener extends Binder {
        @Override
        protected boolean onTransact(int code, Parcel data, Parcel reply, int flags) {
            try {
                switch (code) {
                    case 1: // onListenerConnected(NotificationRankingUpdate)
                        connected = true;
                        return true;
                    case 2: { // onNotificationPosted(holder, rankingUpdate)
                        data.enforceInterface("android.service.notification.INotificationListener");
                        add(readHolder(data), false);
                        return true;
                    }
                    case 4: { // onNotificationRemoved(holder, rankingUpdate, stats, reason)
                        data.enforceInterface("android.service.notification.INotificationListener");
                        add(readHolder(data), true);
                        return true;
                    }
                    default:
                        return true;
                }
            } catch (Throwable t) {
                error = t.getClass().getSimpleName() + ": " + t.getMessage();
                return true;
            }
        }
    }
}
