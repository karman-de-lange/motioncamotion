#include "vpl_decode.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <vpl/mfx.h>
#include <vpl/mfxmemory.h>
#include <vpl/mfxvideo.h>

struct VPLDecoder {
    mfxSession session;
    mfxVideoParam dec_par;
    int dec_inited;
    mfxU32 codec_id;
};

static void downscale_y(const uint8_t *src, int src_w, int src_h, int src_pitch,
                        uint8_t *dst, int dst_w, int dst_h) {
    for (int y = 0; y < dst_h; y++) {
        int sy = y * src_h / dst_h;
        const uint8_t *row = src + sy * src_pitch;
        for (int x = 0; x < dst_w; x++) {
            int sx = x * src_w / dst_w;
            dst[y * dst_w + x] = row[sx];
        }
    }
}

static void fill_rgb_gray(uint8_t *rgb, int w, int h) {
    for (int y = 0; y < h; y++) {
        uint8_t *row = rgb + (size_t)y * w * 3;
        for (int x = 0; x < w; x++) {
            row[x * 3 + 0] = 114;
            row[x * 3 + 1] = 114;
            row[x * 3 + 2] = 114;
        }
    }
}

/* Letterbox into dst (used for fixed-size YOLO 640×640). */
static void nv12_to_rgb(const uint8_t *y, const uint8_t *uv, int pitch, int src_w, int src_h,
                        uint8_t *rgb, int dst_w, int dst_h) {
    fill_rgb_gray(rgb, dst_w, dst_h);

    int scaled_w = dst_w;
    int scaled_h = (src_h * dst_w) / src_w;
    if (scaled_h > dst_h) {
        scaled_h = dst_h;
        scaled_w = (src_w * dst_h) / src_h;
    }
    int pad_x = (dst_w - scaled_w) / 2;
    int pad_y = (dst_h - scaled_h) / 2;

    for (int dy = 0; dy < scaled_h; dy++) {
        int sy = dy * src_h / scaled_h;
        const uint8_t *yrow = y + sy * pitch;
        const uint8_t *uvrow = uv + (sy / 2) * pitch;
        uint8_t *out = rgb + ((size_t)(pad_y + dy) * dst_w + pad_x) * 3;
        for (int dx = 0; dx < scaled_w; dx++) {
            int sx = dx * src_w / scaled_w;
            int yv = (int)yrow[sx] - 16;
            int u = (int)uvrow[sx & ~1] - 128;
            int v = (int)uvrow[(sx & ~1) + 1] - 128;
            int r = (298 * yv + 409 * v + 128) >> 8;
            int g = (298 * yv - 100 * u - 208 * v + 128) >> 8;
            int b = (298 * yv + 516 * u + 128) >> 8;
            out[dx * 3 + 0] = (uint8_t)(r < 0 ? 0 : r > 255 ? 255 : r);
            out[dx * 3 + 1] = (uint8_t)(g < 0 ? 0 : g > 255 ? 255 : g);
            out[dx * 3 + 2] = (uint8_t)(b < 0 ? 0 : b > 255 ? 255 : b);
        }
    }
}

/* Fill entire dst by scaling (dst AR must match source intent — no pads). */
static void nv12_to_rgb_fill(const uint8_t *y, const uint8_t *uv, int pitch, int src_w, int src_h,
                             uint8_t *rgb, int dst_w, int dst_h) {
    for (int dy = 0; dy < dst_h; dy++) {
        int sy = dy * src_h / dst_h;
        const uint8_t *yrow = y + sy * pitch;
        const uint8_t *uvrow = uv ? uv + (sy / 2) * pitch : NULL;
        uint8_t *out = rgb + (size_t)dy * dst_w * 3;
        for (int dx = 0; dx < dst_w; dx++) {
            int sx = dx * src_w / dst_w;
            int yv = (int)yrow[sx] - 16;
            int u = 0, v = 0;
            if (uvrow) {
                u = (int)uvrow[sx & ~1] - 128;
                v = (int)uvrow[(sx & ~1) + 1] - 128;
            }
            int r = (298 * yv + 409 * v + 128) >> 8;
            int g = (298 * yv - 100 * u - 208 * v + 128) >> 8;
            int b = (298 * yv + 516 * u + 128) >> 8;
            out[dx * 3 + 0] = (uint8_t)(r < 0 ? 0 : r > 255 ? 255 : r);
            out[dx * 3 + 1] = (uint8_t)(g < 0 ? 0 : g > 255 ? 255 : g);
            out[dx * 3 + 2] = (uint8_t)(b < 0 ? 0 : b > 255 ? 255 : b);
        }
    }
}

/* Plain luma → grayscale RGB for looksee (no chroma, no histogram tricks). */
static void y_to_rgb_fill(const uint8_t *y, int pitch, int src_w, int src_h,
                          uint8_t *rgb, int dst_w, int dst_h) {
    for (int dy = 0; dy < dst_h; dy++) {
        int sy = dy * src_h / dst_h;
        const uint8_t *yrow = y + sy * pitch;
        uint8_t *out = rgb + (size_t)dy * dst_w * 3;
        for (int dx = 0; dx < dst_w; dx++) {
            int sx = dx * src_w / dst_w;
            uint8_t yy = yrow[sx];
            out[dx * 3 + 0] = yy;
            out[dx * 3 + 1] = yy;
            out[dx * 3 + 2] = yy;
        }
    }
}

static void fit_size(int src_w, int src_h, int max_w, int max_h, int *out_w, int *out_h) {
    int w = src_w;
    int h = src_h;
    if (w > max_w || h > max_h) {
        if ((int64_t)src_w * max_h > (int64_t)max_w * src_h) {
            w = max_w;
            h = (int)((int64_t)src_h * max_w / src_w);
        } else {
            h = max_h;
            w = (int)((int64_t)src_w * max_h / src_h);
        }
    }
    if (w < 2) {
        w = 2;
    }
    if (h < 2) {
        h = 2;
    }
    /* even dims play nicer with codecs / displays */
    w &= ~1;
    h &= ~1;
    *out_w = w;
    *out_h = h;
}

static void release_surf(mfxFrameSurface1 *s) {
    if (s && s->FrameInterface && s->FrameInterface->Release) {
        s->FrameInterface->Release(s);
    }
}

static int ensure_decoder(VPLDecoder *d, const uint8_t *data, size_t len) {
    if (d->dec_inited) {
        return 0;
    }

    mfxBitstream bs;
    memset(&bs, 0, sizeof(bs));
    bs.Data = (mfxU8 *)data;
    bs.DataLength = (mfxU32)len;
    bs.MaxLength = (mfxU32)len;
    /* COMPLETE_FRAME only for H.264 — was not used on working HEVC/Hik path. */
    if (d->codec_id == MFX_CODEC_AVC) {
        bs.DataFlag = MFX_BITSTREAM_COMPLETE_FRAME;
    }

    memset(&d->dec_par, 0, sizeof(d->dec_par));
    d->dec_par.mfx.CodecId = d->codec_id ? d->codec_id : MFX_CODEC_HEVC;
    d->dec_par.IOPattern = MFX_IOPATTERN_OUT_VIDEO_MEMORY;
    /* AsyncDepth=1 was added for H.264; leave default (0) for HEVC like pre-Imou. */
    if (d->codec_id == MFX_CODEC_AVC) {
        d->dec_par.AsyncDepth = 1;
    }

    mfxStatus sts = MFXVideoDECODE_DecodeHeader(d->session, &bs, &d->dec_par);
    if (sts < MFX_ERR_NONE) {
        return -50 - (int)sts;
    }

    sts = MFXVideoDECODE_Init(d->session, &d->dec_par);
    if (sts != MFX_ERR_NONE && sts < MFX_ERR_NONE) {
        return -60 - (int)sts;
    }

    d->dec_inited = 1;
    return 0;
}

/*
 * Original simple HEVC frame decode (pre-Imou). H.264 extras must not live here.
 * Returns 0 + *decoded_out, 1 = need more data, <0 error.
 */
static int decode_one_frame_hevc(VPLDecoder *d, const uint8_t *data, size_t len, mfxFrameSurface1 **decoded_out) {
    int rc = ensure_decoder(d, data, len);
    if (rc != 0) {
        return rc;
    }

    mfxBitstream bs;
    memset(&bs, 0, sizeof(bs));
    bs.Data = (mfxU8 *)data;
    bs.DataLength = (mfxU32)len;
    bs.MaxLength = (mfxU32)len;

    mfxFrameSurface1 *surf = NULL;
    mfxFrameSurface1 *decoded = NULL;
    mfxSyncPoint dec_sync = NULL;

    mfxStatus sts = MFXMemory_GetSurfaceForDecode(d->session, &surf);
    if (sts != MFX_ERR_NONE) {
        return -70 - (int)sts;
    }

    sts = MFXVideoDECODE_DecodeFrameAsync(d->session, &bs, surf, &decoded, &dec_sync);
    if (sts == MFX_ERR_MORE_DATA) {
        release_surf(surf);
        return 1;
    }
    if (sts < MFX_ERR_NONE) {
        release_surf(surf);
        return -80 - (int)sts;
    }

    if (dec_sync) {
        sts = MFXVideoCORE_SyncOperation(d->session, dec_sync, 3000);
        if (sts != MFX_ERR_NONE) {
            release_surf(decoded);
            release_surf(surf);
            return -90 - (int)sts;
        }
    }

    if (!decoded || !decoded->FrameInterface || !decoded->FrameInterface->Map) {
        release_surf(decoded);
        release_surf(surf);
        return -95;
    }

    if (surf && surf != decoded) {
        release_surf(surf);
    }
    *decoded_out = decoded;
    return 0;
}

/* H.264-only: busy retry + null-bitstream drain. */
static int decode_one_frame_avc(VPLDecoder *d, const uint8_t *data, size_t len, mfxFrameSurface1 **decoded_out) {
    int rc = ensure_decoder(d, data, len);
    if (rc != 0) {
        return rc;
    }

    mfxBitstream bs;
    memset(&bs, 0, sizeof(bs));
    bs.Data = (mfxU8 *)data;
    bs.DataLength = (mfxU32)len;
    bs.MaxLength = (mfxU32)len;
    bs.DataFlag = MFX_BITSTREAM_COMPLETE_FRAME;

    for (int pass = 0; pass < 5; pass++) {
        mfxBitstream *pbs = (pass == 0) ? &bs : NULL;
        mfxFrameSurface1 *surf = NULL;
        mfxFrameSurface1 *decoded = NULL;
        mfxSyncPoint dec_sync = NULL;

        mfxStatus sts = MFXMemory_GetSurfaceForDecode(d->session, &surf);
        if (sts < MFX_ERR_NONE) {
            return -70 - (int)sts;
        }

        for (int busy = 0; busy < 50; busy++) {
            sts = MFXVideoDECODE_DecodeFrameAsync(d->session, pbs, surf, &decoded, &dec_sync);
            if (sts != MFX_WRN_DEVICE_BUSY) {
                break;
            }
            if (dec_sync) {
                MFXVideoCORE_SyncOperation(d->session, dec_sync, 50);
                dec_sync = NULL;
            }
        }

        if (sts == MFX_ERR_MORE_DATA || sts == MFX_ERR_MORE_SURFACE) {
            release_surf(surf);
            if (pass == 0) {
                return 1;
            }
            continue;
        }
        if (sts < MFX_ERR_NONE) {
            release_surf(surf);
            return -80 - (int)sts;
        }
        if (!decoded) {
            release_surf(surf);
            continue;
        }
        if (dec_sync) {
            sts = MFXVideoCORE_SyncOperation(d->session, dec_sync, 3000);
            if (sts < MFX_ERR_NONE) {
                release_surf(decoded);
                if (surf != decoded) {
                    release_surf(surf);
                }
                return -90 - (int)sts;
            }
        }
        if (!decoded->FrameInterface || !decoded->FrameInterface->Map) {
            release_surf(decoded);
            if (surf != decoded) {
                release_surf(surf);
            }
            return -95;
        }
        if (surf && surf != decoded) {
            release_surf(surf);
        }
        *decoded_out = decoded;
        return 0;
    }
    return 1;
}

static int decode_one_frame(VPLDecoder *d, const uint8_t *data, size_t len, mfxFrameSurface1 **decoded_out) {
    if (d->codec_id == MFX_CODEC_AVC) {
        return decode_one_frame_avc(d, data, len, decoded_out);
    }
    return decode_one_frame_hevc(d, data, len, decoded_out);
}

int vpl_decoder_new(VPLDecoder **out, int codec) {
    VPLDecoder *d = calloc(1, sizeof(VPLDecoder));
    if (!d) {
        return -1;
    }

    switch (codec) {
    case VPL_CODEC_AVC:
        d->codec_id = MFX_CODEC_AVC;
        break;
    case VPL_CODEC_HEVC:
    default:
        d->codec_id = MFX_CODEC_HEVC;
        break;
    }

    mfxInitializationParam ipar;
    memset(&ipar, 0, sizeof(ipar));
    ipar.AccelerationMode = MFX_ACCEL_MODE_VIA_VAAPI;

    mfxStatus sts = MFXInitialize(ipar, &d->session);
    if (sts != MFX_ERR_NONE) {
        free(d);
        return -10 - (int)sts;
    }

    *out = d;
    return 0;
}

/* Luma plane with crop. Returns 0 on success. UV left for optional color paths. */
static int surface_y(const mfxFrameSurface1 *decoded,
                     const uint8_t **y_out, int *pitch_out, int *w_out, int *h_out) {
    if (!decoded || !decoded->Data.Y) {
        return -1;
    }
    int pitch = decoded->Data.Pitch;
    if (pitch <= 0) {
        return -1;
    }
    int crop_x = decoded->Info.CropX;
    int crop_y = decoded->Info.CropY;
    int w = decoded->Info.CropW ? decoded->Info.CropW : decoded->Info.Width;
    int h = decoded->Info.CropH ? decoded->Info.CropH : decoded->Info.Height;
    if (w <= 0 || h <= 0) {
        return -1;
    }
    *y_out = decoded->Data.Y + (size_t)crop_y * (size_t)pitch + (size_t)crop_x;
    *pitch_out = pitch;
    *w_out = w;
    *h_out = h;
    return 0;
}

/* NV12 chroma for YOLO color path: prefer driver UV, else contiguous after luma. */
static const uint8_t *surface_uv(const mfxFrameSurface1 *decoded, int pitch, int surf_h) {
    if (decoded->Data.UV && decoded->Data.UV != decoded->Data.Y) {
        return decoded->Data.UV;
    }
    if (!decoded->Data.Y || pitch <= 0 || surf_h <= 0) {
        return NULL;
    }
    return decoded->Data.Y + (size_t)pitch * (size_t)surf_h;
}

/* Pre-Imou simple surface read: Crop size + Pitch, driver UV (fallback contiguous). */
static int map_thumb_rgb(mfxFrameSurface1 *decoded,
                         uint8_t *y_out, int y_cap,
                         uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                         int *out_w, int *out_h, int want_rgb) {
    mfxStatus sts = decoded->FrameInterface->Map(decoded, MFX_MAP_READ);
    if (sts != MFX_ERR_NONE) {
        return -100 - (int)sts;
    }
    if (!decoded->Data.Y || decoded->Data.Pitch <= 0) {
        decoded->FrameInterface->Unmap(decoded);
        return -96;
    }

    int pitch = decoded->Data.Pitch;
    int w = decoded->Info.CropW ? decoded->Info.CropW : decoded->Info.Width;
    int h = decoded->Info.CropH ? decoded->Info.CropH : decoded->Info.Height;
    if (w <= 0 || h <= 0) {
        decoded->FrameInterface->Unmap(decoded);
        return -96;
    }

    const uint8_t *y = decoded->Data.Y;
    const uint8_t *uv = decoded->Data.UV;
    if (!uv) {
        int surf_h = decoded->Info.Height > 0 ? (int)decoded->Info.Height : h;
        uv = y + (size_t)pitch * (size_t)surf_h;
    }

    if (y_out && y_cap >= THUMB_Y_SIZE) {
        downscale_y(y, w, h, pitch, y_out, THUMB_W, THUMB_H);
    }
    if (want_rgb && rgb_out && out_w && out_h) {
        int dw = 0, dh = 0;
        fit_size(w, h, max_w, max_h, &dw, &dh);
        if (rgb_cap < dw * dh * 3) {
            decoded->FrameInterface->Unmap(decoded);
            return -2;
        }
        /*
         * Looksee: plain luma only. HEVC Data.UV is often all-zero here → pure green
         * if we NV12-convert. Y plane is fine (that's what motion uses).
         */
        (void)uv;
        y_to_rgb_fill(y, pitch, w, h, rgb_out, dw, dh);
        *out_w = dw;
        *out_h = dh;
    }
    decoded->FrameInterface->Unmap(decoded);
    return 0;
}

int vpl_decoder_thumb(VPLDecoder *d, const uint8_t *data, size_t len, uint8_t *y_out, int y_cap) {
    if (!d || !data || len == 0 || !y_out || y_cap < THUMB_Y_SIZE) {
        return -1;
    }

    mfxFrameSurface1 *decoded = NULL;
    int rc = decode_one_frame(d, data, len, &decoded);
    if (rc != 0) {
        return rc;
    }

    rc = map_thumb_rgb(decoded, y_out, y_cap, NULL, 0, 0, 0, NULL, NULL, 0);
    decoded->FrameInterface->Release(decoded);
    return rc;
}

int vpl_decoder_thumb_rgb_fit(VPLDecoder *d, const uint8_t *data, size_t len,
                              uint8_t *y_out, int y_cap,
                              uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                              int *out_w, int *out_h, int want_rgb) {
    if (!d || !data || len == 0 || !y_out || y_cap < THUMB_Y_SIZE) {
        return -1;
    }

    mfxFrameSurface1 *decoded = NULL;
    int rc = decode_one_frame(d, data, len, &decoded);
    if (rc != 0) {
        return rc;
    }

    rc = map_thumb_rgb(decoded, y_out, y_cap, rgb_out, rgb_cap, max_w, max_h, out_w, out_h, want_rgb);
    decoded->FrameInterface->Release(decoded);
    return rc;
}

int vpl_decoder_rgb(VPLDecoder *d, const uint8_t *data, size_t len, uint8_t *rgb_out, int rgb_cap, int out_w, int out_h) {
    if (!d || !data || len == 0 || !rgb_out || out_w <= 0 || out_h <= 0 || rgb_cap < out_w * out_h * 3) {
        return -1;
    }

    mfxFrameSurface1 *decoded = NULL;
    int rc = decode_one_frame(d, data, len, &decoded);
    if (rc != 0) {
        return rc;
    }

    mfxStatus sts = decoded->FrameInterface->Map(decoded, MFX_MAP_READ);
    if (sts != MFX_ERR_NONE) {
        decoded->FrameInterface->Release(decoded);
        return -100 - (int)sts;
    }

    const uint8_t *y = NULL;
    int pitch = 0, w = 0, h = 0;
    if (surface_y(decoded, &y, &pitch, &w, &h) != 0) {
        decoded->FrameInterface->Unmap(decoded);
        decoded->FrameInterface->Release(decoded);
        return -96;
    }
    int surf_h = decoded->Info.Height > 0 ? (int)decoded->Info.Height : h;
    const uint8_t *uv0 = surface_uv(decoded, pitch, surf_h);
    int crop_x = decoded->Info.CropX;
    int crop_y = decoded->Info.CropY;
    const uint8_t *uv = NULL;
    if (uv0) {
        uv = uv0 + (size_t)(crop_y / 2) * (size_t)pitch + (size_t)(crop_x & ~1);
    }
    if (uv) {
        nv12_to_rgb(y, uv, pitch, w, h, rgb_out, out_w, out_h);
    } else {
        fill_rgb_gray(rgb_out, out_w, out_h);
        int scaled_w = out_w;
        int scaled_h = (h * out_w) / w;
        if (scaled_h > out_h) {
            scaled_h = out_h;
            scaled_w = (w * out_h) / h;
        }
        int pad_x = (out_w - scaled_w) / 2;
        int pad_y = (out_h - scaled_h) / 2;
        for (int dy = 0; dy < scaled_h; dy++) {
            int sy = dy * h / scaled_h;
            const uint8_t *yrow = y + sy * pitch;
            uint8_t *out = rgb_out + ((size_t)(pad_y + dy) * out_w + pad_x) * 3;
            for (int dx = 0; dx < scaled_w; dx++) {
                uint8_t yy = yrow[dx * w / scaled_w];
                out[dx * 3 + 0] = yy;
                out[dx * 3 + 1] = yy;
                out[dx * 3 + 2] = yy;
            }
        }
    }

    decoded->FrameInterface->Unmap(decoded);
    decoded->FrameInterface->Release(decoded);
    return 0;
}

int vpl_decoder_rgb_fit(VPLDecoder *d, const uint8_t *data, size_t len,
                        uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                        int *out_w, int *out_h) {
    if (!d || !data || len == 0 || !rgb_out || max_w <= 0 || max_h <= 0 || !out_w || !out_h) {
        return -1;
    }

    mfxFrameSurface1 *decoded = NULL;
    int rc = decode_one_frame(d, data, len, &decoded);
    if (rc != 0) {
        return rc;
    }

    mfxStatus sts = decoded->FrameInterface->Map(decoded, MFX_MAP_READ);
    if (sts != MFX_ERR_NONE) {
        decoded->FrameInterface->Release(decoded);
        return -100 - (int)sts;
    }
    if (!decoded->Data.Y || decoded->Data.Pitch <= 0) {
        decoded->FrameInterface->Unmap(decoded);
        decoded->FrameInterface->Release(decoded);
        return -96;
    }

    int pitch = decoded->Data.Pitch;
    int sw = decoded->Info.CropW ? decoded->Info.CropW : decoded->Info.Width;
    int sh = decoded->Info.CropH ? decoded->Info.CropH : decoded->Info.Height;
    const uint8_t *y = decoded->Data.Y;
    const uint8_t *uv = decoded->Data.UV;
    if (!uv) {
        int surf_h = decoded->Info.Height > 0 ? (int)decoded->Info.Height : sh;
        uv = y + (size_t)pitch * (size_t)surf_h;
    }

    int dw = 0, dh = 0;
    fit_size(sw, sh, max_w, max_h, &dw, &dh);
    if (rgb_cap < dw * dh * 3) {
        decoded->FrameInterface->Unmap(decoded);
        decoded->FrameInterface->Release(decoded);
        return -2;
    }
    nv12_to_rgb_fill(y, uv, pitch, sw, sh, rgb_out, dw, dh);
    *out_w = dw;
    *out_h = dh;

    decoded->FrameInterface->Unmap(decoded);
    decoded->FrameInterface->Release(decoded);
    return 0;
}

void vpl_decoder_free(VPLDecoder *d) {
    if (!d) {
        return;
    }
    if (d->dec_inited) {
        MFXVideoDECODE_Close(d->session);
    }
    if (d->session) {
        MFXClose(d->session);
    }
    free(d);
}