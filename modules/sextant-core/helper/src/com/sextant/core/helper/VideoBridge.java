package com.sextant.core.helper;

import android.graphics.Bitmap;
import android.media.Image;
import android.media.MediaCodec;
import android.media.MediaFormat;
import java.io.ByteArrayOutputStream;
import java.nio.ByteBuffer;
import org.json.JSONObject;

/** Decodes one scrcpy H.264 access unit into a PNG for virtual-display stills. */
final class VideoBridge {
    private VideoBridge() {}

    static JSONObject decode(String configB64, String frameB64, int width, int height, int timeoutMs) throws Exception {
        byte[] config = android.util.Base64.decode(configB64, android.util.Base64.DEFAULT);
        byte[] frame = android.util.Base64.decode(frameB64, android.util.Base64.DEFAULT);
        if (width <= 0 || height <= 0) throw new IllegalArgumentException("invalid frame size");
        if (timeoutMs <= 0 || timeoutMs > 10000) timeoutMs = 3000;

        MediaCodec codec = null;
        Bitmap bitmap = null;
        try {
            codec = MediaCodec.createDecoderByType("video/avc");
            MediaFormat fmt = MediaFormat.createVideoFormat("video/avc", width, height);
            if (config.length > 0) fmt.setByteBuffer("csd-0", ByteBuffer.wrap(config));
            codec.configure(fmt, null, null, 0);
            codec.start();

            int in = codec.dequeueInputBuffer(10000);
            if (in < 0) throw new IllegalStateException("decoder input unavailable");
            ByteBuffer ib = codec.getInputBuffer(in);
            if (ib == null) throw new IllegalStateException("decoder input buffer null");
            ib.clear();
            ib.put(frame);
            codec.queueInputBuffer(in, 0, frame.length, 0, MediaCodec.BUFFER_FLAG_KEY_FRAME);

            int eos = codec.dequeueInputBuffer(10000);
            if (eos >= 0) {
                codec.queueInputBuffer(eos, 0, 0, 0, MediaCodec.BUFFER_FLAG_END_OF_STREAM);
            }

            MediaCodec.BufferInfo info = new MediaCodec.BufferInfo();
            long deadline = System.currentTimeMillis() + timeoutMs;
            while (System.currentTimeMillis() < deadline) {
                int out = codec.dequeueOutputBuffer(info, 20000);
                if (out >= 0) {
                    Image image = codec.getOutputImage(out);
                    if (image != null) {
                        try {
                            bitmap = toBitmap(image);
                        } finally {
                            image.close();
                        }
                        codec.releaseOutputBuffer(out, false);
                        break;
                    }
                    codec.releaseOutputBuffer(out, false);
                    if ((info.flags & MediaCodec.BUFFER_FLAG_END_OF_STREAM) != 0) break;
                } else if (out == MediaCodec.INFO_TRY_AGAIN_LATER) {
                    Thread.sleep(10);
                }
                // INFO_OUTPUT_FORMAT_CHANGED: keep the ImageReader size; the
                // decoder resolution must match the scrcpy session.
            }
            if (bitmap == null) throw new IllegalStateException("decoder produced no frame");
            ByteArrayOutputStream bos = new ByteArrayOutputStream();
            bitmap.compress(Bitmap.CompressFormat.PNG, 100, bos);
            JSONObject o = new JSONObject();
            o.put("png_base64", android.util.Base64.encodeToString(bos.toByteArray(), android.util.Base64.NO_WRAP));
            o.put("width", bitmap.getWidth());
            o.put("height", bitmap.getHeight());
            return o;
        } finally {
            if (codec != null) {
                try { codec.stop(); } catch (Throwable ignored) {}
                try { codec.release(); } catch (Throwable ignored) {}
            }
        }
    }

    private static Bitmap toBitmap(Image image) {
        int w = image.getWidth(), h = image.getHeight();
        Image.Plane[] planes = image.getPlanes();
        if (planes.length < 3) throw new IllegalStateException("decoder did not return YUV image");
        ByteBuffer yb = planes[0].getBuffer(), ub = planes[1].getBuffer(), vb = planes[2].getBuffer();
        int ys = planes[0].getRowStride(), us = planes[1].getRowStride(), vs = planes[2].getRowStride();
        int yp = planes[0].getPixelStride(), up = planes[1].getPixelStride(), vp = planes[2].getPixelStride();
        int[] pixels = new int[w * h];
        for (int y = 0; y < h; y++) {
            int yrow = y * ys;
            int urow = (y / 2) * us;
            int vrow = (y / 2) * vs;
            for (int x = 0; x < w; x++) {
                int yv = yb.get(yrow + x * yp) & 0xff;
                int uv = ub.get(urow + (x / 2) * up) & 0xff;
                int vv = vb.get(vrow + (x / 2) * vp) & 0xff;
                int c = yv - 16;
                if (c < 0) c = 0;
                int d = uv - 128, e = vv - 128;
                int r = clamp((298 * c + 409 * e + 128) >> 8);
                int g = clamp((298 * c - 100 * d - 208 * e + 128) >> 8);
                int b = clamp((298 * c + 516 * d + 128) >> 8);
                pixels[y * w + x] = 0xff000000 | (r << 16) | (g << 8) | b;
            }
        }
        Bitmap bmp = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888);
        bmp.setPixels(pixels, 0, w, 0, 0, w, h);
        return bmp;
    }

    private static int clamp(int v) { return v < 0 ? 0 : (v > 255 ? 255 : v); }
}
