package com.chopin.core.helper;

import android.content.Context;
import android.media.AudioAttributes;
import android.media.AudioDeviceInfo;
import android.media.AudioFormat;
import android.media.AudioManager;
import android.media.AudioRecord;
import android.media.AudioTrack;
import android.media.MediaRecorder;
import android.os.Build;

import org.json.JSONArray;
import org.json.JSONObject;

/**
 * The ears and the mouth of the device, through AudioManager.
 *
 * This is the helper's half of "the agent can hear and speak". The helper runs as
 * root via app_process, so it can hold a system Context and reach the same
 * AudioManager the Settings app does — the ordinary {@code pm grant} route cannot,
 * because a plain app is refused {@code CAPTURE_AUDIO_OUTPUT} at install time.
 *
 * Two things here are deliberately not "just record":
 *
 * <ul>
 *   <li><b>Silence is the failure mode.</b> A capture without the right permission
 *       or on the wrong device does not throw — it returns zeros. Every capture
 *       therefore reports an RMS and says {@code silent: true} rather than a
 *       cheerful {@code started}. An agent that gets "ok" from a recording it
 *       cannot hear will confidently report a conversation that never happened.</li>
 *   <li><b>Routes are enumerated, not guessed.</b> "Speaker on" means the built-in
 *       speaker is the active output device, so the reply lists the real
 *       {@link AudioDeviceInfo}s and names the one that is live.</li>
 * </ul>
 */
final class AudioBridge {
    private final Context ctx;
    private final AudioManager am;

    AudioBridge(Context ctx) {
        this.ctx = ctx;
        this.am = (AudioManager) ctx.getSystemService(Context.AUDIO_SERVICE);
    }

    /** The microphone state, the output route, and every device the platform sees. */
    JSONObject state() throws Exception {
        JSONObject o = new JSONObject();
        o.put("mic_muted", am.isMicrophoneMute());
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            }
        o.put("speakerphone", am.isSpeakerphoneOn());
        o.put("mode", modeName(am.getMode()));
        o.put("ring_mode", ringName(am.getRingerMode()));
        o.put("music_volume", am.getStreamVolume(AudioManager.STREAM_MUSIC));
        o.put("music_max", am.getStreamMaxVolume(AudioManager.STREAM_MUSIC));
        o.put("in_call", am.getMode() == AudioManager.MODE_IN_CALL
                || am.getMode() == AudioManager.MODE_IN_COMMUNICATION);
        o.put("devices", devicesJson());
        return o;
    }

    /** Every audio device the platform reports, with its type and direction. */
    private JSONArray devicesJson() {
        JSONArray arr = new JSONArray();
        AudioDeviceInfo[] all = am.getDevices(AudioManager.GET_DEVICES_ALL);
        for (AudioDeviceInfo d : all) {
            try {
                JSONObject one = new JSONObject();
                one.put("id", d.getId());
                one.put("type", typeName(d.getType()));
                one.put("input", d.isSource());
                one.put("output", d.isSink());
                CharSequence name = d.getProductName();
                one.put("name", name == null ? "" : name.toString());
                // A device is only useful to an agent if it knows whether it is
                // the one currently carrying sound, not merely present.
                one.put("active", isActive(d));
                arr.put(one);
            } catch (Exception ignored) {
                // One unreadable device must not blank the whole list.
            }
        }
        return arr;
    }

    /**
     * Whether this device is the one currently carrying communication audio.
     *
     * GET_DEVICES_OUTPUTS means "connected", not "in use" — a headset that is
     * plugged in but silent appears there too. The platform's own answer is
     * getCommunicationDevice() (API 31+); below that there is no honest way to
     * tell, so the reply says null rather than guessing.
     */
    private Object isActive(AudioDeviceInfo d) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            try {
                AudioDeviceInfo cur = am.getCommunicationDevice();
                if (cur != null) return cur.getId() == d.getId();
            } catch (Throwable ignored) {
            }
        }
        return JSONObject.NULL;
    }

    /**
     * Sets the microphone mute state and reports whether the platform agreed.
     *
     * The toggle is only meaningful during a call or a VOICE_COMMUNICATION capture;
     * outside those the platform accepts the call and the value does not move, so
     * the reply carries {@code applied: false} instead of a lie.
     */
    JSONObject setMicMute(boolean muted) throws Exception {
        boolean before = am.isMicrophoneMute();
        am.setMicrophoneMute(muted);
        boolean after = am.isMicrophoneMute();
        JSONObject o = new JSONObject();
        o.put("requested", muted);
        o.put("before", before);
        o.put("after", after);
        o.put("applied", before != after || after == muted);
        if (!o.getBoolean("applied")) {
            o.put("reason", "the platform did not change the microphone mute state"
                    + " (outside a call the value is accepted and ignored)");
        }
        return o;
    }

    /**
     * Routes call/communication audio to the speaker or back to the earpiece.
     *
     * MODE_IN_COMMUNICATION is required for setSpeakerphoneOn to reach the call
     * path; setting the flag alone in MODE_NORMAL moves nothing audible.
     */
    JSONObject setSpeakerphone(boolean on) throws Exception {
        boolean prevFlag = am.isSpeakerphoneOn();
        // The mode is deliberately NOT changed here. setSpeakerphoneOn routes the
        // CALL path, and forcing MODE_IN_COMMUNICATION outside a call would interrupt
        // whatever the person is listening to. Callers that need the mode use
        // set_mode explicitly.
        am.setSpeakerphoneOn(on);
        boolean after = am.isSpeakerphoneOn();
        JSONObject o = new JSONObject();
        o.put("requested", on);
        o.put("before", prevFlag);
        o.put("after", after);
        o.put("applied", after == on);
        o.put("mode", modeName(am.getMode()));
        return o;
    }

    /** Puts the audio system into speaker or earpiece mode for a call. */
    JSONObject setMode(String mode) throws Exception {
        int want;
        if ("call".equals(mode)) want = AudioManager.MODE_IN_CALL;
        else if ("communication".equals(mode)) want = AudioManager.MODE_IN_COMMUNICATION;
        else if ("ringtone".equals(mode)) want = AudioManager.MODE_RINGTONE;
        else want = AudioManager.MODE_NORMAL;
        int before = am.getMode();
        am.setMode(want);
        JSONObject o = new JSONObject();
        o.put("before", modeName(before));
        o.put("after", modeName(am.getMode()));
        o.put("applied", am.getMode() == want);
        return o;
    }

    /**
     * Records a short burst from the microphone and reports its level.
     *
     * This is the "can the agent hear the room" probe, distinct from a call
     * recording: it uses VOICE_RECOGNITION so it captures ambient speech at full
     * band rather than the narrow call band, and it returns a number instead of a
     * file so a caller can decide whether to proceed.
     */
    JSONObject listen(int millis, int source) throws Exception {
        if (millis <= 0) millis = 1000;
        if (millis > 15000) millis = 15000;
        int src = MediaRecorder.AudioSource.VOICE_RECOGNITION;
        String srcName = "VOICE_RECOGNITION";
        if (source > 0) { src = source; srcName = "custom:" + source; }

        int rate = 16000;
        int min = AudioRecord.getMinBufferSize(rate, AudioFormat.CHANNEL_IN_MONO,
                AudioFormat.ENCODING_PCM_16BIT);
        if (min <= 0) min = 4096;
        int bufSize = Math.max(min, rate / 10 * 2);
        AudioRecord rec = null;
        try {
            rec = new AudioRecord(src, rate, AudioFormat.CHANNEL_IN_MONO,
                    AudioFormat.ENCODING_PCM_16BIT, bufSize * 2);
        } catch (Throwable t) {
            JSONObject o = new JSONObject();
            o.put("ok", false);
            o.put("reason", "AudioRecord could not be constructed: " + t);
            return o;
        }
        if (rec.getState() != AudioRecord.STATE_INITIALIZED) {
            rec.release();
            JSONObject o = new JSONObject();
            o.put("ok", false);
            o.put("reason", "the platform refused the audio source " + srcName
                    + " (SILENCE on a permitted source would look the same; this is a refusal)");
            return o;
        }
        rec.startRecording();
        short[] buf = new short[bufSize];
        long deadline = System.currentTimeMillis() + millis;
        long sum = 0;
        int count = 0;
        int peak = 0;
        while (System.currentTimeMillis() < deadline) {
            int n = rec.read(buf, 0, buf.length);
            if (n <= 0) break;
            for (int i = 0; i < n; i++) {
                int v = buf[i];
                sum += (long) v * v;
                int a = Math.abs(v);
                if (a > peak) peak = a;
                count++;
            }
        }
        rec.stop();
        rec.release();

        double rms = count > 0 ? Math.sqrt((double) sum / count) : 0;
        JSONObject o = new JSONObject();
        o.put("ok", true);
        o.put("source", srcName);
        o.put("ms", millis);
        o.put("samples", count);
        o.put("rms", Math.round(rms));
        o.put("peak", peak);
        // Below ~8/32768 nothing audible is present; the platform will happily
        // hand back zeros forever, which is why this flag exists.
        o.put("silent", rms < 8);
        if (rms < 8) {
            o.put("reason", "the capture is digital silence: either nobody is talking,"
                    + " the microphone is muted/occupied, or this source needs a permission"
                    + " the process does not hold");
        }
        return o;
    }

    /**
     * Plays raw PCM (16 kHz mono s16le) through the chosen route.
     *
     * Used for the agent's own voice: the PC synthesises speech and hands the
     * samples down, so no TTS engine is needed on the device (this phone has none
     * installed — {@code tts_default_synth} is null).
     */
    JSONObject speak(byte[] pcm, boolean speaker) throws Exception {
        if (pcm == null || pcm.length == 0) {
            JSONObject o = new JSONObject();
            o.put("ok", false);
            o.put("reason", "no audio was supplied");
            return o;
        }
        int rate = 16000;
        int outChannels = AudioFormat.CHANNEL_OUT_MONO;
        int minBuf = AudioTrack.getMinBufferSize(rate, outChannels,
                AudioFormat.ENCODING_PCM_16BIT);
        if (minBuf <= 0) minBuf = 4096;
        int mode = pcm.length > minBuf ? AudioTrack.MODE_STREAM : AudioTrack.MODE_STATIC;
        AudioAttributes attrs = new AudioAttributes.Builder()
                .setUsage(speaker ? AudioAttributes.USAGE_MEDIA
                                  : AudioAttributes.USAGE_VOICE_COMMUNICATION)
                .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                .build();
        AudioTrack track = new AudioTrack.Builder()
                .setAudioAttributes(attrs)
                .setAudioFormat(new AudioFormat.Builder()
                        .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                        .setSampleRate(rate)
                        .setChannelMask(outChannels)
                        .build())
                .setBufferSizeInBytes(Math.max(minBuf, pcm.length))
                .setTransferMode(mode)
                .build();
        if (track.getState() != AudioTrack.STATE_INITIALIZED) {
            track.release();
            JSONObject o = new JSONObject();
            o.put("ok", false);
            o.put("reason", "the platform refused to open an output track");
            return o;
        }
        boolean prevSpeaker = am.isSpeakerphoneOn();
        if (speaker != prevSpeaker) am.setSpeakerphoneOn(speaker);
        String routedEarly = "";
        if (mode == AudioTrack.MODE_STATIC) {
            track.write(pcm, 0, pcm.length);
            track.play();
            routedEarly = routedDeviceName(track);
        } else {
            track.play();
            routedEarly = routedDeviceName(track);
            int off = 0;
            while (off < pcm.length) {
                int n = track.write(pcm, off, Math.min(minBuf, pcm.length - off));
                if (n <= 0) break;
                off += n;
            }
        }
        // Wait for the queue to drain, otherwise the reply arrives before the
        // sound and a caller that plays two clips back to back overlaps them.
        long ms = (long) pcm.length * 1000 / (rate * 2);
        Thread.sleep(Math.min(ms + 120, 30000));
        try {
            track.stop();
        } catch (Throwable ignored) {
        }
        track.release();
        if (speaker != prevSpeaker) am.setSpeakerphoneOn(prevSpeaker);

        JSONObject o = new JSONObject();
        o.put("ok", true);
        o.put("bytes", pcm.length);
        o.put("ms", ms);
        o.put("speaker", speaker);
        o.put("rate", rate);
        o.put("channels", 1);
        o.put("routed_to", routedEarly.isEmpty() ? "unknown" : routedEarly);
        return o;
    }

    /**
     * The device the platform actually routed this track to, by name.
     *
     * getRoutedDevice() is the only honest source: AudioAttributes describe what was
     * ASKED for, and the policy is free to ignore it.
     */
    private String routedDeviceName(AudioTrack track) {
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
                AudioDeviceInfo d = track.getRoutedDevice();
                if (d != null) return typeName(d.getType());
            }
        } catch (Throwable ignored) {
        }
        return "unknown";
    }

    private static String modeName(int mode) {
        switch (mode) {
            case AudioManager.MODE_NORMAL: return "normal";
            case AudioManager.MODE_RINGTONE: return "ringtone";
            case AudioManager.MODE_IN_CALL: return "in_call";
            case AudioManager.MODE_IN_COMMUNICATION: return "in_communication";
            default: return "mode_" + mode;
        }
    }

    private static String ringName(int mode) {
        switch (mode) {
            case AudioManager.RINGER_MODE_SILENT: return "silent";
            case AudioManager.RINGER_MODE_VIBRATE: return "vibrate";
            case AudioManager.RINGER_MODE_NORMAL: return "normal";
            default: return "ring_" + mode;
        }
    }

    private static String typeName(int type) {
        switch (type) {
            case AudioDeviceInfo.TYPE_BUILTIN_EARPIECE: return "earpiece";
            case AudioDeviceInfo.TYPE_BUILTIN_SPEAKER: return "speaker";
            case AudioDeviceInfo.TYPE_BUILTIN_MIC: return "mic";
            case AudioDeviceInfo.TYPE_WIRED_HEADSET: return "wired_headset";
            case AudioDeviceInfo.TYPE_WIRED_HEADPHONES: return "wired_headphones";
            case AudioDeviceInfo.TYPE_BLUETOOTH_SCO: return "bluetooth_sco";
            case AudioDeviceInfo.TYPE_BLUETOOTH_A2DP: return "bluetooth_a2dp";
            case AudioDeviceInfo.TYPE_USB_DEVICE: return "usb_device";
            case AudioDeviceInfo.TYPE_TELEPHONY: return "telephony";
            case AudioDeviceInfo.TYPE_BLE_HEADSET: return "ble_headset";
            case AudioDeviceInfo.TYPE_BLE_SPEAKER: return "ble_speaker";
            case AudioDeviceInfo.TYPE_HEARING_AID: return "hearing_aid";
            default: return "type_" + type;
        }
    }
}
