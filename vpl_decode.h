#pragma once

#include <stddef.h>
#include <stdint.h>

#define THUMB_W 128
#define THUMB_H 128
#define THUMB_Y_SIZE (THUMB_W * THUMB_H)

/* Codec for vpl_decoder_new: HEVC (H.265) or AVC (H.264). */
#define VPL_CODEC_HEVC 0
#define VPL_CODEC_AVC  1

typedef struct VPLDecoder VPLDecoder;

int vpl_decoder_new(VPLDecoder **out, int codec);
int vpl_decoder_thumb(VPLDecoder *d, const uint8_t *data, size_t len, uint8_t *y_out, int y_cap);
/* Tight-pack NV12 (pitch=width). Returns -2 if y/uv caps too small (*out_w/*out_h set). */
int vpl_decoder_nv12(VPLDecoder *d, const uint8_t *data, size_t len,
                     uint8_t *y_out, int y_cap, uint8_t *uv_out, int uv_cap,
                     int *out_w, int *out_h);
/* Letterbox NV12 to dst RGB, pad 114. uv=NULL → limited-range Y as gray. */
void vpl_nv12_letterbox_rgb(const uint8_t *y, const uint8_t *uv, int pitch,
                            int src_w, int src_h, uint8_t *rgb, int dst_w, int dst_h);
int vpl_uv_plane_dead(const uint8_t *uv, int pitch, int src_w, int src_h);
/* Decode one AU and keep the surface. Does not map. Replaces any previous feed. */
int vpl_decoder_feed(VPLDecoder *d, const uint8_t *data, size_t len);
/* Copy the fed surface to tight NV12 and release it. -2 = caps too small, surface kept. */
int vpl_decoder_take_nv12(VPLDecoder *d,
                          uint8_t *y_out, int y_cap, uint8_t *uv_out, int uv_cap,
                          int *out_w, int *out_h);
/* Scale NV12 into max_w×max_h preserving aspect. Writes *out_w/*out_h. */
void vpl_nv12_fit_rgb(const uint8_t *y, const uint8_t *uv, int pitch,
                      int src_w, int src_h, uint8_t *rgb, int max_w, int max_h,
                      int *out_w, int *out_h);
/* Scale into max_w×max_h preserving aspect (no letterbox). Writes *out_w/*out_h. */
int vpl_decoder_rgb_fit(VPLDecoder *d, const uint8_t *data, size_t len,
                        uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                        int *out_w, int *out_h);
/* One decode: motion thumb + optional looksee RGB (same Map — avoids HEVC UV glitch on re-feed). */
int vpl_decoder_thumb_rgb_fit(VPLDecoder *d, const uint8_t *data, size_t len,
                              uint8_t *y_out, int y_cap,
                              uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                              int *out_w, int *out_h, int want_rgb);
void vpl_decoder_free(VPLDecoder *d);