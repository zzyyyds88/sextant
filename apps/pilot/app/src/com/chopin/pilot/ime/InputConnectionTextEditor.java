package com.chopin.pilot.ime;

import android.view.inputmethod.ExtractedText;
import android.view.inputmethod.ExtractedTextRequest;
import android.view.inputmethod.InputConnection;

/**
 * Port of mobilerun-portal 0.8.2 InputConnectionTextEditor.kt.
 *
 * <p>Commits text through the active InputConnection and verifies the field content with
 * ExtractedText. This is the only path that reliably commits Chinese into self-drawn input
 * widgets such as WeChat's composer.</p>
 */
final class InputConnectionTextEditor {
    private static final long DEFAULT_RETRY_DELAY_MS = 100L;
    private static final int MAX_ATTEMPTS = 2;
    private static final char ZERO_WIDTH_SPACE = '\u200B';
    private static final char ZERO_WIDTH_NO_BREAK_SPACE = '\uFEFF';

    interface ConnectionProvider {
        InputConnection get();
    }

    interface GenerationProvider {
        long get();
    }

    interface Sleeper {
        void sleep(long ms);
    }

    private static final class Snapshot {
        final String text;
        final int startOffset;
        final int partialStartOffset;
        final int selectionStart;
        final int selectionEnd;
        final boolean reachesFieldEnd;

        Snapshot(String text, int startOffset, int partialStartOffset,
                 int selectionStart, int selectionEnd, boolean reachesFieldEnd) {
            this.text = text;
            this.startOffset = startOffset;
            this.partialStartOffset = partialStartOffset;
            this.selectionStart = selectionStart;
            this.selectionEnd = selectionEnd;
            this.reachesFieldEnd = reachesFieldEnd;
        }

        boolean isCompleteReport() {
            return partialStartOffset == -1;
        }

        boolean coversEntireField() {
            return startOffset == 0 && isCompleteReport() && reachesFieldEnd;
        }
    }

    private final ConnectionProvider connectionProvider;
    private final GenerationProvider generationProvider;
    private final Sleeper sleeper;
    private final long retryDelayMs;

    InputConnectionTextEditor(ConnectionProvider connectionProvider,
                              GenerationProvider generationProvider,
                              Sleeper sleeper) {
        this(connectionProvider, generationProvider, sleeper, DEFAULT_RETRY_DELAY_MS);
    }

    InputConnectionTextEditor(ConnectionProvider connectionProvider,
                              GenerationProvider generationProvider,
                              Sleeper sleeper,
                              long retryDelayMs) {
        this.connectionProvider = connectionProvider;
        this.generationProvider = generationProvider;
        this.sleeper = sleeper;
        this.retryDelayMs = retryDelayMs;
    }

    TextInputResult inputText(String text, boolean clear) {
        final long session = generationProvider.get();
        boolean sawConnection = false;

        for (int attempt = 0; attempt < MAX_ATTEMPTS; attempt++) {
            if (!isSameInputSession(session)) {
                return TextInputResult.INPUT_SESSION_CHANGED;
            }

            InputConnection connection = connectionOrNull();
            if (connection == null) {
                if (attempt < MAX_ATTEMPTS - 1) {
                    sleeper.sleep(retryDelayMs);
                    continue;
                }
                return sawConnection ? TextInputResult.REJECTED
                        : TextInputResult.NO_INPUT_CONNECTION;
            }
            sawConnection = true;

            if (!isSameInputSession(session)) {
                return TextInputResult.INPUT_SESSION_CHANGED;
            }
            boolean compositionFinished;
            try {
                compositionFinished = connection.finishComposingText();
            } catch (Exception ignored) {
                compositionFinished = false;
            }
            if (!compositionFinished) {
                if (!clear) {
                    TextInputResult result = commitWithoutVerification(connection, text, session);
                    if (result == TextInputResult.REJECTED) {
                        if (attempt < MAX_ATTEMPTS - 1) {
                            sleeper.sleep(retryDelayMs);
                            continue;
                        }
                        return TextInputResult.REJECTED;
                    }
                    return result;
                }
                if (!isSameInputSession(session)) {
                    return TextInputResult.INPUT_SESSION_CHANGED;
                }
                if (attempt < MAX_ATTEMPTS - 1) {
                    sleeper.sleep(retryDelayMs);
                    continue;
                }
                return TextInputResult.REJECTED;
            }

            if (!isSameInputSession(session)) {
                return TextInputResult.INPUT_SESSION_CHANGED;
            }

            Snapshot before = readSnapshot(connection, clear);
            if (before == null && !clear) {
                TextInputResult result = commitWithoutVerification(connection, text, session);
                if (result == TextInputResult.REJECTED) {
                    if (attempt < MAX_ATTEMPTS - 1) {
                        sleeper.sleep(retryDelayMs);
                        continue;
                    }
                    return TextInputResult.REJECTED;
                }
                return result;
            }
            if (before == null || (clear && !before.coversEntireField())) {
                if (attempt < MAX_ATTEMPTS - 1) {
                    sleeper.sleep(retryDelayMs);
                    continue;
                }
                return TextInputResult.REJECTED;
            }

            int[] selection = selectionFor(before, clear);
            String expected = expectedText(before, text, clear);
            boolean selectionAccepted;
            try {
                selectionAccepted = connection.setSelection(selection[0], selection[1]);
            } catch (Exception ignored) {
                selectionAccepted = false;
            }
            if (!selectionAccepted) {
                if (!isSameInputSession(session)) {
                    return TextInputResult.INPUT_SESSION_CHANGED;
                }
                if (attempt < MAX_ATTEMPTS - 1) {
                    sleeper.sleep(retryDelayMs);
                    continue;
                }
                return TextInputResult.REJECTED;
            }

            if (!isSameInputSession(session)) {
                return TextInputResult.INPUT_SESSION_CHANGED;
            }
            boolean commitAccepted;
            try {
                commitAccepted = connection.commitText(text, 1);
            } catch (Exception e) {
                return isSameInputSession(session)
                        ? TextInputResult.COMMIT_OUTCOME_UNKNOWN
                        : TextInputResult.INPUT_SESSION_CHANGED;
            }
            if (!commitAccepted) {
                if (!isSameInputSession(session)) {
                    return TextInputResult.INPUT_SESSION_CHANGED;
                }
                if (attempt < MAX_ATTEMPTS - 1) {
                    sleeper.sleep(retryDelayMs);
                    continue;
                }
                return TextInputResult.REJECTED;
            }

            sleeper.sleep(retryDelayMs);
            if (!isSameInputSession(session)) {
                return TextInputResult.INPUT_SESSION_CHANGED;
            }
            InputConnection afterConnection = connectionOrNull();
            Snapshot after = afterConnection == null ? null
                    : readSnapshot(afterConnection, clear);
            if (!isSameInputSession(session)) {
                return TextInputResult.INPUT_SESSION_CHANGED;
            }
            if (after != null && canVerify(before, after, clear)
                    && matches(after.text, expected, text)) {
                return TextInputResult.VERIFIED;
            }
            return TextInputResult.ACCEPTED_UNVERIFIED;
        }
        return sawConnection ? TextInputResult.REJECTED : TextInputResult.NO_INPUT_CONNECTION;
    }

    private boolean isSameInputSession(long generation) {
        return generationProvider.get() == generation;
    }

    private TextInputResult commitWithoutVerification(InputConnection connection, String text,
                                                      long session) {
        if (!isSameInputSession(session)) {
            return TextInputResult.INPUT_SESSION_CHANGED;
        }
        boolean accepted;
        try {
            accepted = connection.commitText(text, 1);
        } catch (Exception e) {
            return isSameInputSession(session)
                    ? TextInputResult.COMMIT_OUTCOME_UNKNOWN
                    : TextInputResult.INPUT_SESSION_CHANGED;
        }
        if (!isSameInputSession(session)) {
            return TextInputResult.INPUT_SESSION_CHANGED;
        }
        return accepted ? TextInputResult.ACCEPTED_UNVERIFIED : TextInputResult.REJECTED;
    }

    private InputConnection connectionOrNull() {
        try {
            return connectionProvider.get();
        } catch (Exception ignored) {
            return null;
        }
    }

    private Snapshot readSnapshot(InputConnection connection, boolean requireFieldEnd) {
        try {
            ExtractedText extracted = connection.getExtractedText(new ExtractedTextRequest(), 0);
            if (extracted == null) {
                return null;
            }
            String value = extracted.text == null ? "" : extracted.text.toString();
            int relativeStart = extracted.selectionStart;
            int relativeEnd = extracted.selectionEnd;
            if (relativeStart < 0 || relativeEnd < 0
                    || relativeStart > value.length() || relativeEnd > value.length()) {
                return null;
            }
            boolean structurallyComplete =
                    extracted.startOffset == 0 && extracted.partialStartOffset == -1;
            boolean fieldEndConfirmed = !requireFieldEnd
                    || (structurallyComplete
                    && reachesFieldEnd(connection, value, relativeStart, relativeEnd));
            return new Snapshot(value, extracted.startOffset, extracted.partialStartOffset,
                    relativeStart, relativeEnd, fieldEndConfirmed);
        } catch (Exception ignored) {
            return null;
        }
    }

    private boolean reachesFieldEnd(InputConnection connection, String text,
                                    int selectionStart, int selectionEnd) {
        int cursor = Math.max(selectionStart, selectionEnd);
        String representedSuffix = text.substring(cursor);
        CharSequence actual = connection.getTextAfterCursor(representedSuffix.length() + 1, 0);
        if (actual == null) {
            return false;
        }
        return actual.toString().equals(representedSuffix);
    }

    private int[] selectionFor(Snapshot snapshot, boolean clear) {
        int relativeStart = clear ? 0 : Math.min(snapshot.selectionStart, snapshot.selectionEnd);
        int relativeEnd = clear ? snapshot.text.length()
                : Math.max(snapshot.selectionStart, snapshot.selectionEnd);
        return new int[]{snapshot.startOffset + relativeStart,
                snapshot.startOffset + relativeEnd};
    }

    private String expectedText(Snapshot snapshot, String text, boolean clear) {
        if (clear) {
            return text;
        }
        int start = Math.min(snapshot.selectionStart, snapshot.selectionEnd);
        int end = Math.max(snapshot.selectionStart, snapshot.selectionEnd);
        return snapshot.text.substring(0, start) + text + snapshot.text.substring(end);
    }

    private boolean canVerify(Snapshot before, Snapshot after, boolean clear) {
        if (clear) {
            return after.coversEntireField();
        }
        return before.isCompleteReport()
                && after.isCompleteReport()
                && before.startOffset == after.startOffset;
    }

    private boolean matches(String actual, String expected, String requested) {
        if (containsIgnoredSentinel(requested)) {
            return actual.equals(expected);
        }
        return normalize(actual).equals(normalize(expected));
    }

    private boolean containsIgnoredSentinel(String value) {
        for (int i = 0; i < value.length(); i++) {
            if (isIgnoredSentinel(value.charAt(i))) {
                return true;
            }
        }
        return false;
    }

    private String normalize(String value) {
        StringBuilder out = new StringBuilder(value.length());
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (!isIgnoredSentinel(c)) {
                out.append(c);
            }
        }
        return out.toString();
    }

    private boolean isIgnoredSentinel(char value) {
        return value == ZERO_WIDTH_SPACE || value == ZERO_WIDTH_NO_BREAK_SPACE;
    }
}
