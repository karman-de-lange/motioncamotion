#pragma once

#include <stddef.h>
#include <stdint.h>

/* Same thumb / codec constants as vpl_decode.h so motion.go stays aligned. */
#define THUMB_W 128
#define THUMB_H 128
#define THUMB_Y_SIZE (THUMB_W * THUMB_H)

#define NVDEC_CODEC_HEVC 0
#define NVDEC_CODEC_AVC  1

typedef struct NVDECDecoder NVDECDecoder;

int nvdec_decoder_new(NVDECDecoder **out, int codec);
int nvdec_decoder_thumb(NVDECDecoder *d, const uint8_t *data, size_t len, uint8_t *y_out, int y_cap);
int nvdec_decoder_rgb(NVDECDecoder *d, const uint8_t *data, size_t len, uint8_t *rgb_out, int rgb_cap, int out_w, int out_h);
int nvdec_decoder_rgb_fit(NVDECDecoder *d, const uint8_t *data, size_t len,
                          uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                          int *out_w, int *out_h);
void nvdec_decoder_free(NVDECDecoder *d);
