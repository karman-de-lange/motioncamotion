#include "ov_infer.h"

#include <openvino/c/openvino.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

struct OVInfer {
	ov_compiled_model_t *compiled;
	ov_infer_request_t *request;
	int width;
	int height;
	char device[32];
};

static void rgb_to_nchw_f32(const uint8_t *rgb, int w, int h, float *dst) {
	size_t plane = (size_t)w * h;
	for (int y = 0; y < h; y++) {
		for (int x = 0; x < w; x++) {
			size_t i = (size_t)y * w + x;
			dst[0 * plane + i] = rgb[i * 3 + 0] / 255.0f;
			dst[1 * plane + i] = rgb[i * 3 + 1] / 255.0f;
			dst[2 * plane + i] = rgb[i * 3 + 2] / 255.0f;
		}
	}
}

static int class_wanted(int cls) {
	/* person, car, bus, truck + bird/cat/dog/horse/sheep/cow as "animal".
	 * Skip elephant/bear/zebra/giraffe — chairs and tarps map onto those. */
	return cls == 0 || cls == 2 || cls == 5 || cls == 7 ||
	       (cls >= 14 && cls <= 19);
}

static float box_iou(float ax1, float ay1, float ax2, float ay2,
                     float bx1, float by1, float bx2, float by2) {
	float ix1 = ax1 > bx1 ? ax1 : bx1;
	float iy1 = ay1 > by1 ? ay1 : by1;
	float ix2 = ax2 < bx2 ? ax2 : bx2;
	float iy2 = ay2 < by2 ? ay2 : by2;
	float iw = ix2 - ix1;
	float ih = iy2 - iy1;
	if (iw <= 0 || ih <= 0) {
		return 0;
	}
	float inter = iw * ih;
	float aa = (ax2 - ax1) * (ay2 - ay1);
	float ba = (bx2 - bx1) * (by2 - by1);
	float u = aa + ba - inter;
	if (u <= 0) {
		return 0;
	}
	return inter / u;
}

static int nms_same_class(OVDet *dets, int n, float iou_thr) {
	int keep = 0;
	char drop[OV_MAX_DETS * 4];
	for (int i = 0; i < n; i++) {
		drop[i] = 0;
	}
	for (int i = 0; i < n; i++) {
		if (drop[i]) {
			continue;
		}
		dets[keep++] = dets[i];
		for (int j = i + 1; j < n; j++) {
			if (drop[j] || dets[j].class_id != dets[i].class_id) {
				continue;
			}
			if (box_iou(dets[i].x1, dets[i].y1, dets[i].x2, dets[i].y2,
			            dets[j].x1, dets[j].y1, dets[j].x2, dets[j].y2) >= iou_thr) {
				drop[j] = 1;
			}
		}
	}
	return keep;
}

static void load_plugins(ov_core_t *core) {
	/* Prefer env (user-local install); fall back to distro path. */
	const char *dir = getenv("OPENVINO_LIB_DIR");
	if (!dir || !dir[0]) {
		dir = "/usr/lib/openvino-2025.4.0";
	}
	const char *plugins[] = {
		"libopenvino_intel_cpu_plugin.so",
		"libopenvino_intel_gpu_plugin.so",
	};
	char path[512];
	for (size_t i = 0; i < sizeof(plugins) / sizeof(plugins[0]); i++) {
		snprintf(path, sizeof(path), "%s/%s", dir, plugins[i]);
		ov_core_add_extension(core, path);
	}
}

static int compile_model(const char *model_path, const char *device, OVInfer *inf) {
	ov_core_t *core = NULL;
	if (ov_core_create(&core) != OK) {
		return -1;
	}
	load_plugins(core);

	ov_compiled_model_t *compiled = NULL;
	ov_status_e st = ov_core_compile_model_from_file(core, model_path, device, 0, &compiled);
	ov_core_free(core);
	if (st != OK) {
		return (int)st;
	}

	ov_infer_request_t *request = NULL;
	if (ov_compiled_model_create_infer_request(compiled, &request) != OK) {
		ov_compiled_model_free(compiled);
		return -2;
	}

	inf->compiled = compiled;
	inf->request = request;
	strncpy(inf->device, device, sizeof(inf->device) - 1);
	inf->device[sizeof(inf->device) - 1] = '\0';
	return 0;
}

int ov_infer_new(OVInfer **out, const char *model_path, const char *device, int width, int height) {
	if (!out || !model_path || !device || width <= 0 || height <= 0) {
		return -1;
	}

	OVInfer *inf = calloc(1, sizeof(OVInfer));
	if (!inf) {
		return -1;
	}
	inf->width = width;
	inf->height = height;

	int rc = compile_model(model_path, device, inf);
	if (rc != 0 && strcmp(device, "CPU") != 0) {
		rc = compile_model(model_path, "CPU", inf);
	}
	if (rc != 0) {
		free(inf);
		return rc;
	}

	*out = inf;
	return 0;
}

void ov_infer_free(OVInfer *inf) {
	if (!inf) {
		return;
	}
	if (inf->request) {
		ov_infer_request_free(inf->request);
	}
	if (inf->compiled) {
		ov_compiled_model_free(inf->compiled);
	}
	free(inf);
}

const char *ov_infer_device(OVInfer *inf) {
	if (!inf) {
		return "";
	}
	return inf->device;
}

int ov_infer_run(OVInfer *inf, const uint8_t *rgb, float conf,
                 float *out_ms, int *out_pass, float *out_score, int *out_class,
                 float *out_x1, float *out_y1, float *out_x2, float *out_y2) {
	if (!inf || !rgb || !out_ms || !out_pass || !out_score || !out_class ||
	    !out_x1 || !out_y1 || !out_x2 || !out_y2) {
		return -1;
	}

	ov_tensor_t *input = NULL;
	if (ov_infer_request_get_input_tensor(inf->request, &input) != OK) {
		return -2;
	}

	void *input_data = NULL;
	if (ov_tensor_data(input, &input_data) != OK) {
		return -3;
	}

	struct timespec t0, t1;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rgb_to_nchw_f32(rgb, inf->width, inf->height, (float *)input_data);

	if (ov_infer_request_infer(inf->request) != OK) {
		return -4;
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);

	ov_tensor_t *output = NULL;
	if (ov_infer_request_get_output_tensor_by_index(inf->request, 0, &output) != OK) {
		return -5;
	}

	ov_shape_t shape = {.rank = 0, .dims = NULL};
	if (ov_tensor_get_shape(output, &shape) != OK) {
		return -6;
	}
	if (shape.rank != 3 || shape.dims[1] < 5) {
		ov_shape_free(&shape);
		return -7;
	}

	void *output_data = NULL;
	if (ov_tensor_data(output, &output_data) != OK) {
		ov_shape_free(&shape);
		return -8;
	}

	size_t channels = (size_t)shape.dims[1];
	size_t num_det = (size_t)shape.dims[2];
	float *preds = (float *)output_data;

	float best_score = 0.0f;
	int best_cls = -1;
	float best_x1 = 0, best_y1 = 0, best_x2 = 0, best_y2 = 0;
	for (size_t i = 0; i < num_det; i++) {
		float cls_best = 0.0f;
		int cls_id = -1;
		for (size_t c = 4; c < channels; c++) {
			float s = preds[c * num_det + i];
			if (s > cls_best) {
				cls_best = s;
				cls_id = (int)(c - 4);
			}
		}
		if (cls_best < conf || !class_wanted(cls_id)) {
			continue;
		}
		if (cls_best > best_score) {
			float cx = preds[0 * num_det + i];
			float cy = preds[1 * num_det + i];
			float bw = preds[2 * num_det + i];
			float bh = preds[3 * num_det + i];
			best_score = cls_best;
			best_cls = cls_id;
			best_x1 = cx - bw * 0.5f;
			best_y1 = cy - bh * 0.5f;
			best_x2 = cx + bw * 0.5f;
			best_y2 = cy + bh * 0.5f;
		}
	}
	ov_shape_free(&shape);

	*out_ms = (float)(t1.tv_sec - t0.tv_sec) * 1000.0f +
	          (float)(t1.tv_nsec - t0.tv_nsec) / 1000000.0f;
	*out_score = best_score;
	*out_class = best_cls;
	*out_pass = best_cls >= 0 && best_score >= conf;
	*out_x1 = best_x1;
	*out_y1 = best_y1;
	*out_x2 = best_x2;
	*out_y2 = best_y2;
	return 0;
}

static int infer_once(OVInfer *inf, const uint8_t *rgb) {
	ov_tensor_t *input = NULL;
	if (ov_infer_request_get_input_tensor(inf->request, &input) != OK) {
		return -2;
	}
	void *input_data = NULL;
	if (ov_tensor_data(input, &input_data) != OK) {
		return -3;
	}
	rgb_to_nchw_f32(rgb, inf->width, inf->height, (float *)input_data);
	if (ov_infer_request_infer(inf->request) != OK) {
		return -4;
	}
	return 0;
}

static void scan_preds(float *preds, size_t channels, size_t num_det, float *by_cls) {
	for (int k = 0; k < 3; k++) {
		by_cls[k] = 0.0f;
	}
	for (size_t i = 0; i < num_det; i++) {
		for (size_t c = 4; c < channels; c++) {
			int cls_id = (int)(c - 4);
			float s = preds[c * num_det + i];
			if (cls_id == 0 && s > by_cls[0]) {
				by_cls[0] = s;
			} else if (cls_id == 2 && s > by_cls[1]) {
				by_cls[1] = s;
			} else if (cls_id == 15 && s > by_cls[2]) {
				by_cls[2] = s;
			}
		}
	}
}

int ov_infer_class_scores(OVInfer *inf, const uint8_t *rgb,
                          float *person, float *car, float *cat) {
	if (!inf || !rgb || !person || !car || !cat) {
		return -1;
	}

	if (infer_once(inf, rgb) != 0) {
		return -4;
	}

	ov_tensor_t *output = NULL;
	if (ov_infer_request_get_output_tensor_by_index(inf->request, 0, &output) != OK) {
		return -5;
	}
	ov_shape_t shape = {.rank = 0, .dims = NULL};
	if (ov_tensor_get_shape(output, &shape) != OK || shape.rank != 3) {
		ov_shape_free(&shape);
		return -6;
	}
	void *output_data = NULL;
	if (ov_tensor_data(output, &output_data) != OK) {
		ov_shape_free(&shape);
		return -7;
	}

	float scores[3];
	scan_preds((float *)output_data, (size_t)shape.dims[1], (size_t)shape.dims[2], scores);
	ov_shape_free(&shape);
	*person = scores[0];
	*car = scores[1];
	*cat = scores[2];
	return 0;
}

int ov_infer_best_for_class(OVInfer *inf, const uint8_t *rgb, int class_id,
                            float *out_score, float *out_x1, float *out_y1,
                            float *out_x2, float *out_y2) {
	if (!inf || !rgb || !out_score || !out_x1 || !out_y1 || !out_x2 || !out_y2) {
		return -1;
	}
	if (!class_wanted(class_id)) {
		return -1;
	}
	if (infer_once(inf, rgb) != 0) {
		return -2;
	}

	ov_tensor_t *output = NULL;
	if (ov_infer_request_get_output_tensor_by_index(inf->request, 0, &output) != OK) {
		return -3;
	}
	ov_shape_t shape = {.rank = 0, .dims = NULL};
	if (ov_tensor_get_shape(output, &shape) != OK || shape.rank != 3) {
		ov_shape_free(&shape);
		return -4;
	}
	void *output_data = NULL;
	if (ov_tensor_data(output, &output_data) != OK) {
		ov_shape_free(&shape);
		return -5;
	}

	size_t channels = (size_t)shape.dims[1];
	size_t num_det = (size_t)shape.dims[2];
	float *preds = (float *)output_data;

	float best_score = 0.0f;
	float x1 = 0, y1 = 0, x2 = 0, y2 = 0;
	for (size_t i = 0; i < num_det; i++) {
		float s = preds[((size_t)4 + class_id) * num_det + i];
		if (s <= best_score) {
			continue;
		}
		float cx = preds[0 * num_det + i];
		float cy = preds[1 * num_det + i];
		float bw = preds[2 * num_det + i];
		float bh = preds[3 * num_det + i];
		best_score = s;
		x1 = cx - bw * 0.5f;
		y1 = cy - bh * 0.5f;
		x2 = cx + bw * 0.5f;
		y2 = cy + bh * 0.5f;
	}
	ov_shape_free(&shape);

	if (best_score <= 0.0f) {
		return 1;
	}
	*out_score = best_score;
	*out_x1 = x1;
	*out_y1 = y1;
	*out_x2 = x2;
	*out_y2 = y2;
	return 0;
}

int ov_infer_run_multi(OVInfer *inf, const uint8_t *rgb, float conf,
                       float *out_ms, OVDet *out_dets, int max_dets, int *out_n) {
	if (!inf || !rgb || !out_ms || !out_dets || max_dets <= 0 || !out_n) {
		return -1;
	}
	*out_n = 0;

	ov_tensor_t *input = NULL;
	if (ov_infer_request_get_input_tensor(inf->request, &input) != OK) {
		return -2;
	}
	void *input_data = NULL;
	if (ov_tensor_data(input, &input_data) != OK) {
		return -3;
	}

	struct timespec t0, t1;
	clock_gettime(CLOCK_MONOTONIC, &t0);
	rgb_to_nchw_f32(rgb, inf->width, inf->height, (float *)input_data);
	if (ov_infer_request_infer(inf->request) != OK) {
		return -4;
	}
	clock_gettime(CLOCK_MONOTONIC, &t1);

	ov_tensor_t *output = NULL;
	if (ov_infer_request_get_output_tensor_by_index(inf->request, 0, &output) != OK) {
		return -5;
	}
	ov_shape_t shape = {.rank = 0, .dims = NULL};
	if (ov_tensor_get_shape(output, &shape) != OK) {
		return -6;
	}
	if (shape.rank != 3 || shape.dims[1] < 5) {
		ov_shape_free(&shape);
		return -7;
	}
	void *output_data = NULL;
	if (ov_tensor_data(output, &output_data) != OK) {
		ov_shape_free(&shape);
		return -8;
	}

	size_t channels = (size_t)shape.dims[1];
	size_t num_det = (size_t)shape.dims[2];
	float *preds = (float *)output_data;

	/* Collect then partial-sort by score (insertion for small N). */
	OVDet tmp[OV_MAX_DETS * 4];
	int ntmp = 0;
	int cap_tmp = (int)(sizeof(tmp) / sizeof(tmp[0]));
	for (size_t i = 0; i < num_det; i++) {
		float cls_best = 0.0f;
		int cls_id = -1;
		for (size_t c = 4; c < channels; c++) {
			float s = preds[c * num_det + i];
			if (s > cls_best) {
				cls_best = s;
				cls_id = (int)(c - 4);
			}
		}
		if (cls_best < conf || !class_wanted(cls_id) || ntmp >= cap_tmp) {
			continue;
		}
		float cx = preds[0 * num_det + i];
		float cy = preds[1 * num_det + i];
		float bw = preds[2 * num_det + i];
		float bh = preds[3 * num_det + i];
		tmp[ntmp].score = cls_best;
		tmp[ntmp].class_id = cls_id;
		tmp[ntmp].x1 = cx - bw * 0.5f;
		tmp[ntmp].y1 = cy - bh * 0.5f;
		tmp[ntmp].x2 = cx + bw * 0.5f;
		tmp[ntmp].y2 = cy + bh * 0.5f;
		ntmp++;
	}
	ov_shape_free(&shape);

	for (int i = 1; i < ntmp; i++) {
		OVDet key = tmp[i];
		int j = i - 1;
		while (j >= 0 && tmp[j].score < key.score) {
			tmp[j + 1] = tmp[j];
			j--;
		}
		tmp[j + 1] = key;
	}

	ntmp = nms_same_class(tmp, ntmp, 0.45f);
	int n = ntmp < max_dets ? ntmp : max_dets;
	for (int i = 0; i < n; i++) {
		out_dets[i] = tmp[i];
	}
	*out_n = n;
	*out_ms = (float)(t1.tv_sec - t0.tv_sec) * 1000.0f +
	          (float)(t1.tv_nsec - t0.tv_nsec) / 1000000.0f;
	return 0;
}