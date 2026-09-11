"""Tiny text-to-speech HTTP service around Piper (offline neural TTS).

POST /synthesize  {"text": "..."}  -> audio/wav
GET  /healthz                      -> {"status":"ok","voice":"..."}
"""
import io
import json
import os
import sys
import threading
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from piper import PiperVoice

VOICE = os.environ.get("PIPER_VOICE", "en_US-lessac-medium")
VOICE_DIR = os.environ.get("PIPER_VOICE_DIR", "/voices")
MAX_CHARS = int(os.environ.get("TTS_MAX_CHARS", "600"))

voice = PiperVoice.load(os.path.join(VOICE_DIR, VOICE + ".onnx"))
lock = threading.Lock()  # one synthesis at a time keeps memory flat on small boxes


class Handler(BaseHTTPRequestHandler):
    def _json(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            return self._json(200, {"status": "ok", "voice": VOICE})
        self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/synthesize":
            return self._json(404, {"error": "not found"})
        try:
            n = int(self.headers.get("Content-Length", "0"))
            req = json.loads(self.rfile.read(n) or b"{}")
            text = str(req.get("text", "")).strip()
        except (ValueError, json.JSONDecodeError):
            return self._json(400, {"error": "invalid json"})
        if not text:
            return self._json(400, {"error": "text is required"})
        if len(text) > MAX_CHARS:
            return self._json(400, {"error": f"text longer than {MAX_CHARS} characters"})
        buf = io.BytesIO()
        with lock, wave.open(buf, "wb") as w:
            voice.synthesize_wav(text, w)
        data = buf.getvalue()
        self.send_response(200)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):  # one line per request, no client noise
        sys.stdout.write("%s %s\n" % (self.address_string(), fmt % args))
        sys.stdout.flush()


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "5000"))
    print(f"tts listening on :{port} voice={VOICE}", flush=True)
    ThreadingHTTPServer(("", port), Handler).serve_forever()
