package com.chopin.pilot.ime;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.database.Cursor;
import android.net.Uri;
import android.os.Binder;
import android.os.Bundle;
import android.util.Base64;
import android.util.Log;

/** Root/shell-only control surface for the chopin-pilot IME. */
public final class ImeControlProvider extends ContentProvider {
    private static final String TAG = "ChopinImeControl";

    @Override
    public boolean onCreate() {
        return true;
    }

    private void enforceAuthorizedCaller() {
        int uid = Binder.getCallingUid();
        int ownUid = getContext().getApplicationInfo().uid;
        if (uid != 0 && uid != 2000 && uid != ownUid) {
            throw new SecurityException("caller uid " + uid + " is not allowed");
        }
    }

    private static Bundle result(boolean ok, TextInputResult value, String reason) {
        Bundle bundle = new Bundle();
        bundle.putBoolean("ok", ok);
        bundle.putString("result", value == null ? "ERROR" : value.name());
        if (reason != null) {
            bundle.putString("reason", reason);
        }
        return bundle;
    }

    private static TextInputResult parseResult(String raw) {
        try {
            return TextInputResult.valueOf(raw);
        } catch (Exception ignored) {
            return TextInputResult.REJECTED;
        }
    }

    @Override
    public Bundle call(String method, String arg, Bundle extras) {
        enforceAuthorizedCaller();
        RemoteImeService service = RemoteImeService.getInstance();
        if (service == null) {
            return result(false, TextInputResult.NO_INPUT_CONNECTION, "ime_not_running");
        }

        if ("ping".equals(method)) {
            return result(true, TextInputResult.VERIFIED, null);
        }
        if ("status".equals(method)) {
            boolean connected = service.hasInputConnection();
            return result(connected,
                    connected ? TextInputResult.VERIFIED : TextInputResult.NO_INPUT_CONNECTION,
                    connected ? null : "no_input_connection");
        }
        if ("input".equals(method)) {
            String text;
            try {
                byte[] decoded = Base64.decode(arg == null ? "" : arg, Base64.DEFAULT);
                text = new String(decoded, "UTF-8");
            } catch (Exception e) {
                return result(false, TextInputResult.REJECTED, "bad_base64");
            }
            boolean clear = extras != null && extras.getBoolean("clear", false);
            TextInputResult value = service.inputText(text, clear);
            return result(value == TextInputResult.VERIFIED, value,
                    value == TextInputResult.VERIFIED ? null : value.name().toLowerCase());
        }
        if ("clear".equals(method)) {
            TextInputResult value = service.clearText();
            return result(value == TextInputResult.VERIFIED, value,
                    value == TextInputResult.VERIFIED ? null : value.name().toLowerCase());
        }
        if ("key".equals(method)) {
            int keyCode;
            try {
                keyCode = Integer.parseInt(arg == null ? "" : arg.trim());
            } catch (Exception e) {
                return result(false, TextInputResult.REJECTED, "bad_keycode");
            }
            boolean ok = service.sendKeyEventDirect(keyCode);
            return result(ok, ok ? TextInputResult.VERIFIED : TextInputResult.REJECTED,
                    ok ? null : "key_rejected");
        }
        return result(false, TextInputResult.REJECTED, "unknown_method");
    }

    @Override
    public Cursor query(Uri uri, String[] projection, String selection,
                        String[] selectionArgs, String sortOrder) {
        enforceAuthorizedCaller();
        return null;
    }

    @Override
    public String getType(Uri uri) {
        return null;
    }

    @Override
    public Uri insert(Uri uri, ContentValues values) {
        enforceAuthorizedCaller();
        Log.w(TAG, "insert is not used; call input instead");
        return null;
    }

    @Override
    public int delete(Uri uri, String selection, String[] selectionArgs) {
        enforceAuthorizedCaller();
        return 0;
    }

    @Override
    public int update(Uri uri, ContentValues values, String selection, String[] selectionArgs) {
        enforceAuthorizedCaller();
        return 0;
    }
}
