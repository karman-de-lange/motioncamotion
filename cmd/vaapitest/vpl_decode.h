#pragma once

#include <stddef.h>
#include <stdint.h>

#define THUMB_W 32
#define THUMB_H 32
#define THUMB_Y_SIZE (THUMB_W * THUMB_H)

typedef struct VPLDecoder VPLDecoder;

int vpl_decoder_new(VPLDecoder **out);
int vpl_decoder_thumb(VPLDecoder *d, const uint8_t *data, size_t len, uint8_t *y_out, int y_cap);
void vpl_decoder_free(VPLDecoder *d);