package com.sextant.pilot;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.charset.StandardCharsets;

/**
 * The one and only privileged call this app is allowed to make.
 *
 * <p>DESIGN D21 removed the app's root: the module starts the daemon as root, so
 * nothing privileged has to run from here. The single exception is writing the
 * unlock credential, which must reach a root-only path and must be possible
 * while the phone is locked (no computer, no network). Passing the value on
 * stdin keeps it out of {@code ps} and out of shell history.
 */
final class Shell {

    private static final int TIMEOUT_MS = 20_000;

    static final class Result {
        final boolean ok;
        final String output;

        Result(boolean ok, String output) {
            this.ok = ok;
            this.output = output;
        }
    }

    private Shell() {}

    /**
     * Stores the screen pattern: {@code su -c 'sextant-core ctl secret set-unlock'}
     * with the value piped in.
     *
     * <p>The returned text never contains the credential: the CLI prints
     * {@code stored} and nothing else.
     */
    static Result storePattern(String digits) {
        return runWithInput(EnginePaths.CLI + " ctl secret set-unlock", digits + "\n");
    }

    /** Deletes the stored credential. */
    static Result clearPattern() {
        return run(EnginePaths.CLI + " ctl secret clear-unlock");
    }

    /** Reports credential presence only (never the value). */
    static Result secretStatus() {
        return run(EnginePaths.CLI + " ctl secret status");
    }

    private static Result run(String command) {
        return runWithInput(command, null);
    }

    private static Result runWithInput(String command, String input) {
        Process p = null;
        try {
            p = new ProcessBuilder("su", "-c", command).redirectErrorStream(true).start();
            if (input != null) {
                try (OutputStream stdin = p.getOutputStream()) {
                    stdin.write(input.getBytes(StandardCharsets.UTF_8));
                    stdin.flush();
                }
            } else {
                p.getOutputStream().close();
            }
            String out = readAll(p.getInputStream());
            if (!p.waitFor(TIMEOUT_MS, java.util.concurrent.TimeUnit.MILLISECONDS)) {
                p.destroyForcibly();
                return new Result(false, "命令超时");
            }
            boolean ok = p.exitValue() == 0;
            return new Result(ok, out.trim());
        } catch (Throwable t) {
            return new Result(false, String.valueOf(t));
        } finally {
            if (p != null) p.destroy();
        }
    }

    private static String readAll(InputStream in) throws Exception {
        ByteArrayOutputStream buf = new ByteArrayOutputStream();
        byte[] chunk = new byte[4096];
        int n;
        while ((n = in.read(chunk)) > 0) buf.write(chunk, 0, n);
        return buf.toString("UTF-8");
    }
}
