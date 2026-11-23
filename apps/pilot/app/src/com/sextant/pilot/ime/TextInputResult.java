package com.sextant.pilot.ime;

/** Result of one text commit attempt, mirroring Portal's verified input contract. */
public enum TextInputResult {
    VERIFIED,
    REJECTED,
    ACCEPTED_UNVERIFIED,
    COMMIT_OUTCOME_UNKNOWN,
    INPUT_SESSION_CHANGED,
    NO_INPUT_CONNECTION
}
