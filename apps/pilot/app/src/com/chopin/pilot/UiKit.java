package com.chopin.pilot;

import android.content.Context;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * The status page's small design system, in code.
 *
 * The app ships no XML layouts and no image resources — it is assembled with
 * LayoutParams so that a rebuild needs nothing but javac. That made every screen a
 * pile of ad-hoc TextViews with hand-picked sizes. This file is the one place those
 * decisions live now.
 *
 * The values are the same ones the daemon's WebUI uses (the mist neutrals, the
 * brick-rose danger, the same type steps), because these are two views of one
 * product: the person holding the phone and the person watching the browser should
 * recognise each other's screen. Colours resolve light/dark from the system, since
 * this page is read in a pocket as often as at a desk.
 */
final class UiKit {
    private UiKit() {}

    // ---- palette --------------------------------------------------------
    // Read from the current configuration rather than a resource qualifier: the
    // app has no values-night tree, and adding one for six colours would be more
    // files than this page has screens.
    static boolean dark(Context c) {
        int mode = c.getResources().getConfiguration().uiMode
                & android.content.res.Configuration.UI_MODE_NIGHT_MASK;
        return mode == android.content.res.Configuration.UI_MODE_NIGHT_YES;
    }

    static int surface(Context c)      { return dark(c) ? 0xFF151920 : 0xFFF6F7FB; }
    static int surfaceCard(Context c)  { return dark(c) ? 0xFF1D222B : 0xFFFFFFFF; }
    static int hairline(Context c)     { return dark(c) ? 0x26FFFFFF : 0x1F0B152D; }
    static int ink(Context c)          { return dark(c) ? 0xFFE9ECF2 : 0xFF191E29; }
    static int ink2(Context c)         { return dark(c) ? 0xFFB9C0CC : 0xFF454C5C; }
    static int ink3(Context c)         { return dark(c) ? 0xFF9BA3B1 : 0xFF5F6674; }
    static int accent(Context c)       { return dark(c) ? 0xFF9DB4EE : 0xFF3D5AA8; }
    static int accentWeak(Context c)   { return dark(c) ? 0xFF2A3149 : 0xFFDEE8FF; }
    static int ok(Context c)           { return dark(c) ? 0xFF7FD3A0 : 0xFF2C6B45; }
    static int okWeak(Context c)       { return dark(c) ? 0xFF1E3228 : 0xFFDCF0E4; }
    static int warn(Context c)         { return dark(c) ? 0xFFE4BE76 : 0xFF7A5312; }
    static int warnWeak(Context c)     { return dark(c) ? 0xFF322915 : 0xFFFBEFD5; }
    static int danger(Context c)       { return dark(c) ? 0xFFEC9A94 : 0xFF9C3B37; }
    static int dangerWeak(Context c)   { return dark(c) ? 0xFF38201F : 0xFFFBE5E3; }

    // ---- measurement ----------------------------------------------------
    static int dp(Context c, float v) {
        return Math.round(TypedValue.applyDimension(
                TypedValue.COMPLEX_UNIT_DIP, v, c.getResources().getDisplayMetrics()));
    }

    /** A rounded surface with a hairline border: the only card shape here. */
    static GradientDrawable card(Context c, int fill, float radiusDp) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(fill);
        d.setCornerRadius(dp(c, radiusDp));
        d.setStroke(Math.max(1, dp(c, 1)), hairline(c));
        return d;
    }

    static GradientDrawable pill(Context c, int fill) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(fill);
        d.setCornerRadius(dp(c, 999));
        return d;
    }

    // ---- text -----------------------------------------------------------

    /** Body text. 14sp minimum: this is read on a phone at arm's length. */
    static TextView body(Context c, String text) {
        TextView v = new TextView(c);
        v.setText(text);
        v.setTextSize(14);
        v.setTextColor(ink(c));
        return v;
    }

    /** A label above a value: small, uppercase-ish, never the thing you read first. */
    static TextView label(Context c, String text) {
        TextView v = new TextView(c);
        v.setText(text);
        v.setTextSize(11);
        v.setTextColor(ink3(c));
        v.setLetterSpacing(0.06f);
        return v;
    }

    /** A measurement. Monospace so digits line up between refreshes. */
    static TextView value(Context c, String text, int color) {
        TextView v = new TextView(c);
        v.setText(text);
        v.setTextSize(13);
        v.setTypeface(Typeface.MONOSPACE);
        v.setTextColor(color);
        return v;
    }

    static TextView heading(Context c, String text) {
        TextView v = new TextView(c);
        v.setText(text);
        v.setTextSize(19);
        v.setTypeface(Typeface.DEFAULT_BOLD);
        v.setTextColor(ink(c));
        v.setLetterSpacing(-0.01f);
        return v;
    }

    /**
     * A status chip: dot plus word, tinted by state.
     *
     * The dot matters more than the word — a row of these is scanned, not read, and
     * colour is what makes the odd one out visible.
     */
    static TextView chip(Context c, String text, int fg, int bg) {
        TextView v = new TextView(c);
        v.setText("●  " + text);
        v.setTextSize(12);
        v.setTextColor(fg);
        v.setBackground(pill(c, bg));
        v.setPadding(dp(c, 10), dp(c, 5), dp(c, 11), dp(c, 5));
        return v;
    }

    /** A reading: label over value, in a cell that shares the row evenly. */
    static LinearLayout cell(Context c, String label, TextView valueView) {
        LinearLayout box = new LinearLayout(c);
        box.setOrientation(LinearLayout.VERTICAL);
        box.addView(label(c, label));
        box.addView(valueView);
        return box;
    }

    static LinearLayout row(Context c) {
        LinearLayout r = new LinearLayout(c);
        r.setOrientation(LinearLayout.HORIZONTAL);
        r.setGravity(Gravity.CENTER_VERTICAL);
        return r;
    }

    static LinearLayout column(Context c) {
        LinearLayout r = new LinearLayout(c);
        r.setOrientation(LinearLayout.VERTICAL);
        return r;
    }

    /** Adds a view with even weight so cells share a row without fixed widths. */
    static void addWeighted(LinearLayout parent, View v, float weight) {
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0,
                ViewGroup.LayoutParams.WRAP_CONTENT, weight);
        parent.addView(v, lp);
    }

    static void gap(Context c, LinearLayout parent, float dpHeight) {
        View spacer = new View(c);
        parent.addView(spacer, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(c, dpHeight)));
    }

    static int blend(int a, int b, float t) {
        float u = 1f - t;
        int r = Math.round(Color.red(a) * u + Color.red(b) * t);
        int g = Math.round(Color.green(a) * u + Color.green(b) * t);
        int bl = Math.round(Color.blue(a) * u + Color.blue(b) * t);
        return Color.rgb(r, g, bl);
    }
}
