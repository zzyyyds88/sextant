package com.sextant.pilot;

/**
 * Writes the device's own unlock credential (screen pattern).
 *
 * <p>The credential has to be settable while the phone is locked and possibly
 * without any network, so it cannot come from the computer: the person holding
 * the phone types it here, the value goes to {@code su} on <b>stdin</b> (never as
 * an argument, which is visible in {@code ps} to every process), the daemon's CLI
 * writes it 0600 owned by root, and the daemon reads it in-process when it needs
 * to unlock.
 *
 * <p>Nothing here reads the value back. {@link #status()} reports presence only.
 */
final class UnlockSecrets {

    private UnlockSecrets() {}

    static final class Result {
        final boolean ok;
        final String message;

        Result(boolean ok, String message) {
            this.ok = ok;
            this.message = message;
        }
    }

    /**
     * Stores a screen pattern.
     *
     * <p>Accepted input is 1-9 digit characters; anything else is stripped, the
     * same normalisation the daemon applies when it reads the file. Fewer than
     * four dots cannot be an Android pattern, so it is rejected here instead of
     * being discovered at unlock time on a locked phone.
     */
    static Result storePattern(String raw) {
        StringBuilder digits = new StringBuilder();
        if (raw != null) {
            for (int i = 0; i < raw.length(); i++) {
                char c = raw.charAt(i);
                if (c >= '1' && c <= '9') digits.append(c);
            }
        }
        if (digits.length() < 4) {
            return new Result(false, "图案至少 4 个点（当前 " + digits.length() + "）");
        }
        Shell.Result r = Shell.storePattern(digits.toString());
        if (!r.ok) {
            return new Result(false, "保存失败: " + r.output);
        }
        return new Result(true, "已保存（" + digits.length() + " 个点）；值不回显");
    }

    /** Deletes the stored credential. */
    static Result clearPattern() {
        Shell.Result r = Shell.clearPattern();
        return new Result(r.ok, r.ok ? "已删除" : "删除失败: " + r.output);
    }

    /**
     * Credential presence from the daemon's status file.
     *
     * <p>Deliberately not a shell call: the app should not need root just to draw
     * a line on the page, and the status document already carries the answer.
     */
    static boolean credentialPresent() {
        PilotStatus st = PilotStatus.read();
        return st.available && "present".equals(st.unlockCredential);
    }
}
