"""Tiny text-to-speech HTTP service around Piper (offline neural TTS).

GET  /voices                                  -> {"voices":[{id,label,language,quality}], "default": id}
POST /synthesize  {"text": "...", "voice": id}  -> audio/wav   (voice optional)
GET  /healthz                                 -> {"status":"ok"}

Voices are the .onnx files in PIPER_VOICE_DIR (downloaded at image build time).
Models load on first use and only the most recently used few stay in memory.
"""
import collections
import io
import json
import os
import sys
import threading
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from piper import PiperVoice

VOICE_DIR = os.environ.get("PIPER_VOICE_DIR", "/voices")
DEFAULT_VOICE = os.environ.get("PIPER_DEFAULT_VOICE", "en_US-hfc_female-medium")
MAX_CHARS = int(os.environ.get("TTS_MAX_CHARS", "600"))
MAX_LOADED = int(os.environ.get("TTS_MAX_LOADED_VOICES", "2"))

# Friendly names for the voices we ship; anything else gets a generated label.
LABELS = {
    "en_US-hfc_female-medium": "Female · US English (natural)",
    "en_US-hfc_male-medium": "Male · US English (natural)",
    "en_US-lessac-medium": "Female · US English (clear)",
    "en_US-ryan-high": "Male · US English (high quality)",
    "en_GB-jenny_dioco-medium": "Female · British English",
    "en_GB-alan-medium": "Male · British English",
}


def discover():
    voices = {}
    for f in sorted(os.listdir(VOICE_DIR)):
        if not f.endswith(".onnx"):
            continue
        vid = f[:-5]
        meta = {}
        try:
            with open(os.path.join(VOICE_DIR, f + ".json")) as fh:
                meta = json.load(fh)
        except (OSError, json.JSONDecodeError):
            pass
        lang = (meta.get("language") or {}).get("name_english") or vid.split("-")[0]
        region = (meta.get("language") or {}).get("region") or ""
        quality = (meta.get("audio") or {}).get("quality") or vid.rsplit("-", 1)[-1]
        label = LABELS.get(vid) or "%s · %s %s" % (vid.split("-")[1].replace("_", " ").title(), region, lang)
        voices[vid] = {"id": vid, "label": label, "language": lang, "region": region, "quality": quality}
    return voices


VOICES = discover()
if not VOICES:
    sys.exit("no voices found in " + VOICE_DIR)
if DEFAULT_VOICE not in VOICES:
    DEFAULT_VOICE = next(iter(VOICES))

loaded = collections.OrderedDict()  # voice id -> PiperVoice, LRU
lock = threading.Lock()  # one synthesis at a time keeps memory flat on small boxes


def get_voice(vid):
    if vid in loaded:
        loaded.move_to_end(vid)
        return loaded[vid]
    v = PiperVoice.load(os.path.join(VOICE_DIR, vid + ".onnx"))
    loaded[vid] = v
    while len(loaded) > MAX_LOADED:
        loaded.popitem(last=False)
    return v


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
            return self._json(200, {"status": "ok", "voices": len(VOICES), "default": DEFAULT_VOICE})
        if self.path == "/voices":
            return self._json(200, {"voices": list(VOICES.values()), "default": DEFAULT_VOICE})
        self._json(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/synthesize":
            return self._json(404, {"error": "not found"})
        try:
            n = int(self.headers.get("Content-Length", "0"))
            req = json.loads(self.rfile.read(n) or b"{}")
            text = str(req.get("text", "")).strip()
            vid = str(req.get("voice") or DEFAULT_VOICE)
        except (ValueError, json.JSONDecodeError):
            return self._json(400, {"error": "invalid json"})
        if not text:
            return self._json(400, {"error": "text is required"})
        if len(text) > MAX_CHARS:
            return self._json(400, {"error": f"text longer than {MAX_CHARS} characters"})
        if vid not in VOICES:
            return self._json(400, {"error": f"unknown voice {vid!r}"})
        buf = io.BytesIO()
        with lock:
            voice = get_voice(vid)
            with wave.open(buf, "wb") as w:
                voice.synthesize_wav(text, w)
        data = buf.getvalue()
        self.send_response(200)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("X-Voice", vid)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, fmt, *args):  # one line per request, no client noise
        sys.stdout.write("%s %s\n" % (self.address_string(), fmt % args))
        sys.stdout.flush()


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "5000"))
    print(f"tts listening on :{port} voices={list(VOICES)} default={DEFAULT_VOICE}", flush=True)
    get_voice(DEFAULT_VOICE)  # warm the default so the first announcement is quick
    ThreadingHTTPServer(("", port), Handler).serve_forever()
