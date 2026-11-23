package com.sextant.pilot.ime;

import android.graphics.Color;
import android.inputmethodservice.InputMethodService;
import android.os.SystemClock;
import android.util.Log;
import android.view.KeyEvent;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.TextView;
import android.view.inputmethod.InputConnection;

/**
 * Headless IME used by the Sextant Pilot engine to commit text through InputConnection.
 *
 * <p>Derived from mobilerun-portal 0.8.2 MobilerunKeyboardIME.kt (AGPL-3.0).</p>
 */
public final class RemoteImeService extends InputMethodService {
    private static final String TAG = "RemoteImeService";
    private static volatile RemoteImeService instance;
    private volatile long inputGeneration;

    private final InputConnectionTextEditor textEditor = new InputConnectionTextEditor(
            new InputConnectionTextEditor.ConnectionProvider() {
                @Override
                public InputConnection get() {
                    return getCurrentInputConnection();
                }
            },
            new InputConnectionTextEditor.GenerationProvider() {
                @Override
                public long get() {
                    return inputGeneration;
                }
            },
            new InputConnectionTextEditor.Sleeper() {
                @Override
                public void sleep(long ms) {
                    SystemClock.sleep(ms);
                }
            });

    public static RemoteImeService getInstance() {
        return instance;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        instance = this;
        Log.d(TAG, "onCreate");
    }

    @Override
    public void onDestroy() {
        instance = null;
        Log.d(TAG, "onDestroy");
        super.onDestroy();
    }

    @Override
    public void onStartInput(android.view.inputmethod.EditorInfo attribute, boolean restarting) {
        inputGeneration++;
        super.onStartInput(attribute, restarting);
        Log.d(TAG, "onStartInput restarting=" + restarting);
    }

    @Override
    public void onFinishInput() {
        inputGeneration++;
        super.onFinishInput();
    }

    @Override
    public View onCreateInputView() {
        // A headless IME: keep a valid 1dp window so the host app does not
        // relayout into a blank surface, but never cover the content.
        int height = Math.max(1, (int) (48 * getResources().getDisplayMetrics().density));
        TextView view = new TextView(this);
        view.setText("Sextant Pilot input");
        view.setTextSize(10);
        view.setGravity(Gravity.CENTER);
        view.setTextColor(Color.GRAY);
        view.setBackgroundColor(Color.TRANSPARENT);
        view.setLayoutParams(new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, height));
        return view;
    }

    public boolean hasInputConnection() {
        return getCurrentInputConnection() != null;
    }

    public TextInputResult inputText(String text, boolean clear) {
        try {
            TextInputResult result = textEditor.inputText(text, clear);
            Log.d(TAG, "inputText len=" + text.length() + " clear=" + clear
                    + " result=" + result);
            return result;
        } catch (Exception e) {
            Log.e(TAG, "inputText failed", e);
            return TextInputResult.REJECTED;
        }
    }

    public TextInputResult clearText() {
        return inputText("", true);
    }

    public boolean sendKeyEventDirect(int keyCode) {
        try {
            InputConnection connection = getCurrentInputConnection();
            if (connection == null) {
                return false;
            }
            connection.sendKeyEvent(new KeyEvent(KeyEvent.ACTION_DOWN, keyCode));
            connection.sendKeyEvent(new KeyEvent(KeyEvent.ACTION_UP, keyCode));
            Log.d(TAG, "keyevent=" + keyCode);
            return true;
        } catch (Exception e) {
            Log.e(TAG, "sendKeyEventDirect failed", e);
            return false;
        }
    }
}
