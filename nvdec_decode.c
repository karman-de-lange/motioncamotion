//go:build nvidia_nvdec && !darwin

/*
 * NVIDIA NVDEC FrameDecoder backend (WSL/Linux + CUDA).
 * Returns same codes as VPL path: 0=OK, 1=more data, other=error.
 */

#include "nvdec_decode.h"

#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "third_party/ffnvcodec/dynlink_loader.h"

struct NVDECDecoder {
	int codec; /* NVDEC_CODEC_* */
	CUvideoparser parser;
	CUvideodecoder decoder;
	CUVIDEOFORMAT format;
	int have_format;
	int width;
	int height;
	int pitch;

	/* Latest host NV12 frame (Y + UV). */
	uint8_t *nv12;
	int nv12_size;
	int have_frame;
	int more_data; /* set when parse produced no display frame */

	/* Temporary device pointers from Map — only valid during display cb. */
};

/* One CUDA context for the process (serialized — multi-cam starts race otherwise). */
static pthread_mutex_t g_mu = PTHREAD_MUTEX_INITIALIZER;
static CudaFunctions *g_cuda;
static CuvidFunctions *g_cuvid;
static CUcontext g_ctx;
static int g_cuda_ready;
static int g_cuda_fail;

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
	if (w < 2) w = 2;
	if (h < 2) h = 2;
	w &= ~1;
	h &= ~1;
	*out_w = w;
	*out_h = h;
}

/* Caller must hold g_mu. */
static int ensure_cuda_locked(void) {
	if (g_cuda_ready) return 0;
	if (g_cuda_fail) return -1;

	if (cuda_load_functions(&g_cuda, NULL) < 0) {
		g_cuda_fail = 1;
		return -1;
	}
	if (cuvid_load_functions(&g_cuvid, NULL) < 0) {
		g_cuda_fail = 1;
		return -1;
	}
	CUresult r = g_cuda->cuInit(0);
	if (r != CUDA_SUCCESS) {
		fprintf(stderr, "nvdec: cuInit %d\n", (int)r);
		g_cuda_fail = 1;
		return -1;
	}
	CUdevice dev = 0;
	r = g_cuda->cuDeviceGet(&dev, 0);
	if (r != CUDA_SUCCESS) {
		g_cuda_fail = 1;
		return -1;
	}
	r = g_cuda->cuCtxCreate(&g_ctx, 0, dev);
	if (r != CUDA_SUCCESS) {
		/* try primary ctx (WSL) */
		r = g_cuda->cuDevicePrimaryCtxRetain(&g_ctx, dev);
		if (r != CUDA_SUCCESS) {
			fprintf(stderr, "nvdec: cuCtxCreate/primary %d\n", (int)r);
			g_cuda_fail = 1;
			return -1;
		}
	}
	g_cuda_ready = 1;
	return 0;
}

static int CUDAAPI handle_sequence(void *user, CUVIDEOFORMAT *fmt) {
	NVDECDecoder *d = (NVDECDecoder *)user;
	if (!d || !fmt) return 0;

	d->format = *fmt;
	d->have_format = 1;
	d->width = fmt->display_area.right - fmt->display_area.left;
	d->height = fmt->display_area.bottom - fmt->display_area.top;
	if (d->width <= 0) d->width = (int)fmt->coded_width;
	if (d->height <= 0) d->height = (int)fmt->coded_height;

	if (d->decoder) {
		g_cuvid->cuvidDestroyDecoder(d->decoder);
		d->decoder = NULL;
	}

	CUVIDDECODECREATEINFO info;
	memset(&info, 0, sizeof(info));
	info.CodecType = fmt->codec;
	info.ChromaFormat = fmt->chroma_format;
	info.OutputFormat = cudaVideoSurfaceFormat_NV12;
	info.bitDepthMinus8 = fmt->bit_depth_luma_minus8;
	info.ulWidth = fmt->coded_width;
	info.ulHeight = fmt->coded_height;
	info.ulNumDecodeSurfaces = fmt->min_num_decode_surfaces ? fmt->min_num_decode_surfaces : 8;
	if (info.ulNumDecodeSurfaces < 4) info.ulNumDecodeSurfaces = 8;
	info.ulTargetWidth = fmt->coded_width;
	info.ulTargetHeight = fmt->coded_height;
	info.ulNumOutputSurfaces = 2;
	info.ulCreationFlags = cudaVideoCreate_PreferCUVID;
	info.vidLock = NULL;

	CUresult r = g_cuvid->cuvidCreateDecoder(&d->decoder, &info);
	if (r != CUDA_SUCCESS) {
		fprintf(stderr, "nvdec: cuvidCreateDecoder %d\n", (int)r);
		return 0;
	}
	/* Override DPB size to match surfaces. */
	return (int)info.ulNumDecodeSurfaces;
}

static int CUDAAPI handle_decode(void *user, CUVIDPICPARAMS *pic) {
	NVDECDecoder *d = (NVDECDecoder *)user;
	if (!d || !d->decoder || !pic) return 0;
	CUresult r = g_cuvid->cuvidDecodePicture(d->decoder, pic);
	return r == CUDA_SUCCESS ? 1 : 0;
}

static int CUDAAPI handle_display(void *user, CUVIDPARSERDISPINFO *info) {
	NVDECDecoder *d = (NVDECDecoder *)user;
	if (!d || !d->decoder || !info) return 0;

	CUVIDPROCPARAMS proc;
	memset(&proc, 0, sizeof(proc));
	proc.progressive_frame = info->progressive_frame;
	proc.top_field_first = info->top_field_first;
	proc.unpaired_field = info->repeat_first_field < 0 ? 1 : 0;

	CUdeviceptr dpSrcFrame = 0;
	unsigned int pitch = 0;
	CUresult r = g_cuvid->cuvidMapVideoFrame(d->decoder, info->picture_index, &dpSrcFrame, &pitch, &proc);
	if (r != CUDA_SUCCESS) {
		return 0;
	}

	int w = d->width > 0 ? d->width : (int)d->format.coded_width;
	int h = d->height > 0 ? d->height : (int)d->format.coded_height;
	if (w <= 0 || h <= 0 || pitch == 0) {
		g_cuvid->cuvidUnmapVideoFrame(d->decoder, dpSrcFrame);
		return 0;
	}
	/* NV12: Y plane pitch*h + UV pitch*h/2 */
	int need = (int)pitch * h + (int)pitch * ((h + 1) / 2);
	if (need > d->nv12_size) {
		free(d->nv12);
		d->nv12 = (uint8_t *)malloc((size_t)need);
		d->nv12_size = d->nv12 ? need : 0;
	}
	if (!d->nv12) {
		g_cuvid->cuvidUnmapVideoFrame(d->decoder, dpSrcFrame);
		return 0;
	}

	r = g_cuda->cuCtxPushCurrent(g_ctx);
	if (r != CUDA_SUCCESS) {
		g_cuvid->cuvidUnmapVideoFrame(d->decoder, dpSrcFrame);
		return 0;
	}
	r = g_cuda->cuMemcpyDtoH(d->nv12, dpSrcFrame, (size_t)need);
	g_cuda->cuCtxPopCurrent(NULL);
	g_cuvid->cuvidUnmapVideoFrame(d->decoder, dpSrcFrame);
	if (r != CUDA_SUCCESS) {
		return 0;
	}

	d->pitch = (int)pitch;
	d->width = w;
	d->height = h;
	d->have_frame = 1;
	return 1;
}

static int create_parser(NVDECDecoder *d) {
	CUVIDPARSERPARAMS pp;
	memset(&pp, 0, sizeof(pp));
	pp.CodecType = (d->codec == NVDEC_CODEC_AVC) ? cudaVideoCodec_H264 : cudaVideoCodec_HEVC;
	pp.ulMaxNumDecodeSurfaces = 8;
	pp.ulMaxDisplayDelay = 0; /* low latency for live RTSP */
	pp.pUserData = d;
	pp.pfnSequenceCallback = handle_sequence;
	pp.pfnDecodePicture = handle_decode;
	pp.pfnDisplayPicture = handle_display;

	CUresult r = g_cuvid->cuvidCreateVideoParser(&d->parser, &pp);
	if (r != CUDA_SUCCESS) {
		fprintf(stderr, "nvdec: cuvidCreateVideoParser %d\n", (int)r);
		return -1;
	}
	return 0;
}

int nvdec_decoder_new(NVDECDecoder **out, int codec) {
	if (!out) return -1;
	*out = NULL;

	pthread_mutex_lock(&g_mu);
	if (ensure_cuda_locked() != 0) {
		pthread_mutex_unlock(&g_mu);
		return -100;
	}

	NVDECDecoder *d = (NVDECDecoder *)calloc(1, sizeof(NVDECDecoder));
	if (!d) {
		pthread_mutex_unlock(&g_mu);
		return -1;
	}
	d->codec = (codec == NVDEC_CODEC_AVC) ? NVDEC_CODEC_AVC : NVDEC_CODEC_HEVC;

	CUresult r = g_cuda->cuCtxPushCurrent(g_ctx);
	if (r != CUDA_SUCCESS) {
		free(d);
		pthread_mutex_unlock(&g_mu);
		return -101;
	}
	int prc = create_parser(d);
	g_cuda->cuCtxPopCurrent(NULL);
	pthread_mutex_unlock(&g_mu);

	if (prc != 0) {
		free(d);
		return -102;
	}
	*out = d;
	return 0;
}

/* Feed one annex-B AU. Sets have_frame if a picture was displayed. Caller holds g_mu. */
static int feed_au_locked(NVDECDecoder *d, const uint8_t *data, size_t len) {
	if (!d || !data || len == 0 || !d->parser) return -1;

	d->have_frame = 0;

	CUVIDSOURCEDATAPACKET pkt;
	memset(&pkt, 0, sizeof(pkt));
	pkt.payload = data;
	pkt.payload_size = (unsigned long)len;
	pkt.flags = CUVID_PKT_TIMESTAMP;
	pkt.timestamp = 0;

	CUresult r = g_cuda->cuCtxPushCurrent(g_ctx);
	if (r != CUDA_SUCCESS) return -2;
	r = g_cuvid->cuvidParseVideoData(d->parser, &pkt);
	g_cuda->cuCtxPopCurrent(NULL);
	if (r != CUDA_SUCCESS) {
		return -10 - (int)r;
	}
	if (d->have_frame) return 0;
	return 1; /* more data / no display yet */
}

int nvdec_decoder_thumb(NVDECDecoder *d, const uint8_t *data, size_t len, uint8_t *y_out, int y_cap) {
	if (!d || !y_out || y_cap < THUMB_Y_SIZE) return -1;
	pthread_mutex_lock(&g_mu);
	int rc = feed_au_locked(d, data, len);
	if (rc == 0) {
		downscale_y(d->nv12, d->width, d->height, d->pitch, y_out, THUMB_W, THUMB_H);
	}
	pthread_mutex_unlock(&g_mu);
	return rc;
}

int nvdec_decoder_rgb(NVDECDecoder *d, const uint8_t *data, size_t len, uint8_t *rgb_out, int rgb_cap, int out_w, int out_h) {
	if (!d || !rgb_out || out_w <= 0 || out_h <= 0 || rgb_cap < out_w * out_h * 3) return -1;
	pthread_mutex_lock(&g_mu);
	int rc = feed_au_locked(d, data, len);
	if (rc == 0) {
		const uint8_t *y = d->nv12;
		const uint8_t *uv = d->nv12 + (size_t)d->pitch * (size_t)d->height;
		nv12_to_rgb(y, uv, d->pitch, d->width, d->height, rgb_out, out_w, out_h);
	}
	pthread_mutex_unlock(&g_mu);
	return rc;
}

int nvdec_decoder_rgb_fit(NVDECDecoder *d, const uint8_t *data, size_t len,
                          uint8_t *rgb_out, int rgb_cap, int max_w, int max_h,
                          int *out_w, int *out_h) {
	if (!d || !rgb_out || max_w <= 0 || max_h <= 0 || !out_w || !out_h) return -1;
	pthread_mutex_lock(&g_mu);
	int rc = feed_au_locked(d, data, len);
	if (rc == 0) {
		int dw = 0, dh = 0;
		fit_size(d->width, d->height, max_w, max_h, &dw, &dh);
		if (rgb_cap < dw * dh * 3) {
			rc = -2;
		} else {
			const uint8_t *y = d->nv12;
			const uint8_t *uv = d->nv12 + (size_t)d->pitch * (size_t)d->height;
			nv12_to_rgb_fill(y, uv, d->pitch, d->width, d->height, rgb_out, dw, dh);
			*out_w = dw;
			*out_h = dh;
		}
	}
	pthread_mutex_unlock(&g_mu);
	return rc;
}

void nvdec_decoder_free(NVDECDecoder *d) {
	if (!d) return;
	pthread_mutex_lock(&g_mu);
	if (g_cuda_ready && g_cuda) {
		g_cuda->cuCtxPushCurrent(g_ctx);
		if (d->decoder && g_cuvid) g_cuvid->cuvidDestroyDecoder(d->decoder);
		if (d->parser && g_cuvid) g_cuvid->cuvidDestroyVideoParser(d->parser);
		g_cuda->cuCtxPopCurrent(NULL);
	}
	pthread_mutex_unlock(&g_mu);
	free(d->nv12);
	free(d);
}
