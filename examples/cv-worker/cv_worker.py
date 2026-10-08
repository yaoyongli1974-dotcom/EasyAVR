#!/usr/bin/env python3
"""Example CV worker for the EasyAVR AI Middle Platform.

The platform's HTTPDetector sends:
    POST /
    {"mediaUrl": "...", "frame": "<base64 jpeg>", "model": "...", "config": {...}}

and expects a detection result (or a list of them):
    {"eventType": "...", "level": "info|warning|critical",
     "confidence": 0.0, "summary": "...", "payload": {...}}

This reference implementation is dependency-free (stdlib only) and always
reports a generic detection so you can validate the end-to-end pipeline.
Replace `detect()` with a real model (YOLO / PaddleDetection / ...).

Run:  python3 cv_worker.py --port 9000
"""
import argparse
import base64
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def detect(payload):
    frame = payload.get("frame")
    has_frame = bool(frame)
    return {
        "eventType": "object_detected",
        "level": "info",
        "confidence": 0.5,
        "summary": f"参考 CV worker：收到帧={'是' if has_frame else '否'} mediaUrl={payload.get('mediaUrl')}",
        "payload": {
            "model": payload.get("model", "example"),
            "frameBytes": len(base64.b64decode(frame)) if has_frame else 0,
            "ts": int(time.time()),
        },
    }


class Handler(BaseHTTPRequestHandler):
    def _send(self, obj, status=200):
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length) if length else b"{}"
        try:
            payload = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            self._send({"error": "invalid json"}, 400)
            return
        self._send(detect(payload))

    def log_message(self, fmt, *args):
        print("[cv_worker]", fmt % args)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=9000)
    args = parser.parse_args()
    print(f"[cv_worker] listening on http://{args.host}:{args.port}")
    ThreadingHTTPServer((args.host, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
