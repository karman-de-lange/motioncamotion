#ifndef OV_INFER_H
#define OV_INFER_H

#include <stdint.h>

typedef struct OVInfer OVInfer;

/* Returns 0 on success. On failure tries CPU if device was GPU. */
int ov_infer_new(OVInfer **out, const char *model_path, const char *device, int width, int height);

void ov_infer_free(OVInfer *inf);

/* classes: person=0 car=2 cat=15. bbox in model input pixels. out_ms is infer only. */
int ov_infer_run(OVInfer *inf, const uint8_t *rgb, float conf,
                 float *out_ms, int *out_pass, float *out_score, int *out_class,
                 float *out_x1, float *out_y1, float *out_x2, float *out_y2);

const char *ov_infer_device(OVInfer *inf);

/* Best score per wanted class (0=person,2=car,15=cat), regardless of conf. */
int ov_infer_class_scores(OVInfer *inf, const uint8_t *rgb,
                          float *person, float *car, float *cat);

/* Best detection for one class id; returns 0 if none. */
int ov_infer_best_for_class(OVInfer *inf, const uint8_t *rgb, int class_id,
                            float *out_score, float *out_x1, float *out_y1,
                            float *out_x2, float *out_y2);

#define OV_MAX_DETS 32

typedef struct {
	float score;
	int class_id;
	float x1, y1, x2, y2; /* model input pixels */
} OVDet;

/* All wanted-class dets with score >= conf, sorted by score desc, up to max_dets.
 * out_n is number written (0 if none). Returns 0 on success. */
int ov_infer_run_multi(OVInfer *inf, const uint8_t *rgb, float conf,
                       float *out_ms, OVDet *out_dets, int max_dets, int *out_n);

#endif