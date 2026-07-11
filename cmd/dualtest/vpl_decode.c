#include "vpl_decode.h"

#include <stdlib.h>
#include <string.h>

#include <vpl/mfx.h>
#include <vpl/mfxmemory.h>
#include <vpl/mfxvideo.h>

struct VPLDecoder {
    mfxSession session;
    mfxVideoParam dec_par;
    int dec_inited;
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

static int ensure_decoder(VPLDecoder *d, const uint8_t *data, size_t len) {
    if (d->dec_inited) {
        return 0;
    }

    mfxBitstream bs;
    memset(&bs, 0, sizeof(bs));
    bs.Data = (mfxU8 *)data;
    bs.DataLength = (mfxU32)len;
    bs.MaxLength = (mfxU32)len;

    memset(&d->dec_par, 0, sizeof(d->dec_par));
    d->dec_par.mfx.CodecId = MFX_CODEC_HEVC;
    d->dec_par.IOPattern = MFX_IOPATTERN_OUT_VIDEO_MEMORY;

    mfxStatus sts = MFXVideoDECODE_DecodeHeader(d->session, &bs, &d->dec_par);
    if (sts < MFX_ERR_NONE) {
        return -50 - (int)sts;
    }

    sts = MFXVideoDECODE_Init(d->session, &d->dec_par);
    if (sts != MFX_ERR_NONE) {
        return -60 - (int)sts;
    }

    d->dec_inited = 1;
    return 0;
}

int vpl_decoder_new(VPLDecoder **out) {
    VPLDecoder *d = calloc(1, sizeof(VPLDecoder));
    if (!d) {
        return -1;
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

int vpl_decoder_thumb(VPLDecoder *d, const uint8_t *data, size_t len, uint8_t *y_out, int y_cap) {
    if (!d || !data || len == 0 || !y_out || y_cap < THUMB_Y_SIZE) {
        return -1;
    }

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
        if (surf && surf->FrameInterface && surf->FrameInterface->Release) {
            surf->FrameInterface->Release(surf);
        }
        return 1;
    }
    if (sts < MFX_ERR_NONE) {
        if (surf && surf->FrameInterface && surf->FrameInterface->Release) {
            surf->FrameInterface->Release(surf);
        }
        return -80 - (int)sts;
    }

    if (dec_sync) {
        sts = MFXVideoCORE_SyncOperation(d->session, dec_sync, 3000);
        if (sts != MFX_ERR_NONE) {
            if (decoded && decoded->FrameInterface && decoded->FrameInterface->Release) {
                decoded->FrameInterface->Release(decoded);
            }
            if (surf && surf->FrameInterface && surf->FrameInterface->Release) {
                surf->FrameInterface->Release(surf);
            }
            return -90 - (int)sts;
        }
    }

    if (!decoded || !decoded->FrameInterface || !decoded->FrameInterface->Map) {
        if (decoded && decoded->FrameInterface && decoded->FrameInterface->Release) {
            decoded->FrameInterface->Release(decoded);
        }
        if (surf && surf->FrameInterface && surf->FrameInterface->Release) {
            surf->FrameInterface->Release(surf);
        }
        return -95;
    }

    sts = decoded->FrameInterface->Map(decoded, MFX_MAP_READ);
    if (sts != MFX_ERR_NONE) {
        if (decoded->FrameInterface->Release) {
            decoded->FrameInterface->Release(decoded);
        }
        if (surf && surf->FrameInterface && surf->FrameInterface->Release) {
            surf->FrameInterface->Release(surf);
        }
        return -100 - (int)sts;
    }

    int w = decoded->Info.CropW ? decoded->Info.CropW : decoded->Info.Width;
    int h = decoded->Info.CropH ? decoded->Info.CropH : decoded->Info.Height;
    downscale_y(decoded->Data.Y, w, h, decoded->Data.Pitch, y_out, THUMB_W, THUMB_H);

    decoded->FrameInterface->Unmap(decoded);
    if (decoded->FrameInterface->Release) {
        decoded->FrameInterface->Release(decoded);
    }
    if (surf && surf->FrameInterface && surf->FrameInterface->Release) {
        surf->FrameInterface->Release(surf);
    }

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