#!/usr/bin/env python3
"""Read raw RGB bytes from stdin, run YOLOv8 ONNX, print one JSON line."""

import argparse
import json
import sys
import time

import numpy as np
import onnxruntime as ort

COCO = (
    "person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck",
    "boat", "traffic light", "fire hydrant", "stop sign", "parking meter", "bench",
    "bird", "cat", "dog", "horse", "sheep", "cow", "elephant", "bear", "zebra",
    "giraffe", "backpack", "umbrella", "handbag", "tie", "suitcase", "frisbee",
    "skis", "snowboard", "sports ball", "kite", "baseball bat", "baseball glove",
    "skateboard", "surfboard", "tennis racket", "bottle", "wine glass", "cup",
    "fork", "knife", "spoon", "bowl", "banana", "apple", "sandwich", "orange",
    "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair", "couch",
    "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse",
    "remote", "keyboard", "cell phone", "microwave", "oven", "toaster", "sink",
    "refrigerator", "book", "clock", "vase", "scissors", "teddy bear", "hair drier",
    "toothbrush",
)


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--model", required=True)
    p.add_argument("--width", type=int, default=640)
    p.add_argument("--height", type=int, default=640)
    p.add_argument("--conf", type=float, default=0.5)
    p.add_argument("--classes", default="0,2,15", help="comma-separated class ids")
    args = p.parse_args()

    want = {int(x) for x in args.classes.split(",") if x}
    nbytes = args.width * args.height * 3
    raw = sys.stdin.buffer.read(nbytes)
    if len(raw) != nbytes:
        print(json.dumps({"error": f"expected {nbytes} bytes, got {len(raw)}"}))
        return 1

    img = np.frombuffer(raw, dtype=np.uint8).reshape(args.height, args.width, 3)
    blob = img.astype(np.float32) / 255.0
    blob = np.transpose(blob, (2, 0, 1))[None]

    t0 = time.perf_counter()
    sess = ort.InferenceSession(args.model, providers=["CPUExecutionProvider"])
    inp = sess.get_inputs()[0].name
    out = sess.run(None, {inp: blob})[0]
    ms = (time.perf_counter() - t0) * 1000

    preds = np.squeeze(out).T
    best_score = 0.0
    best_cls = -1
    hits = []
    for row in preds:
        scores = row[4:]
        cls = int(np.argmax(scores))
        score = float(scores[cls])
        if score < args.conf or cls not in want:
            continue
        hits.append({"class_id": cls, "class_name": COCO[cls], "score": round(score, 3)})
        if score > best_score:
            best_score = score
            best_cls = cls

    hits.sort(key=lambda h: h["score"], reverse=True)
    print(json.dumps({
        "ms": round(ms, 1),
        "pass": best_score >= args.conf,
        "max_score": round(best_score, 3),
        "class_id": best_cls,
        "class_name": COCO[best_cls] if best_cls >= 0 else "",
        "detections": hits[:5],
    }))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())