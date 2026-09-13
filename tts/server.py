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

from piper import PiperVoice, SynthesisConfig

VOICE_DIR = os.environ.get("PIPER_VOICE_DIR", "/voices")
DEFAULT_VOICE = os.environ.get("PIPER_DEFAULT_VOICE", "en_US-norman-medium")
MAX_CHARS = int(os.environ.get("TTS_MAX_CHARS", "600"))
MAX_LOADED = int(os.environ.get("TTS_MAX_LOADED_VOICES", "2"))

# Friendly names for the voices we ship (Piper's ids are dataset names, so
# the ones without a person's name get one here); anything else gets a
# generated label. Order here is the order the UI shows.
LABELS = {
    "en_US-norman-medium": "Norman · US, male",
    "en_US-amy-medium": "Amy · US, female",
    "en_US-hfc_female-medium": "Hannah · US, female (natural)",
    "en_US-hfc_male-medium": "Henry · US, male (natural)",
    "en_US-joe-medium": "Joe · US, male",
    "en_US-kristin-medium": "Kristin · US, female",
    "en_US-mike-medium": "Mike · US, male",
    "en_US-ryan-high": "Ryan · US, male (high quality)",
    "en_US-lessac-high": "Leslie · US, female (high quality)",
    "en_US-libritts_r-medium": "Libby · US (clear)",
    "en_US-libritts-high": "Liberty · US (high quality)",
    "en_US-l2arctic-medium": "Lina · US, non-native accent",
    "en_GB-jenny_dioco-medium": "Jenny · British, female",
    "en_GB-alan-medium": "Alan · British, male",
}

# Multi-speaker models (libritts has 904 voices, l2arctic 24): which speaker
# each label refers to. Single-speaker models ignore this.
SPEAKERS = {
    "en_US-libritts_r-medium": 0,
    "en_US-libritts-high": 0,
    "en_US-l2arctic-medium": 0,
}


def discover():
    voices = {}
    # Labelled voices first, in LABELS order; anything else after, alphabetically.
    files = sorted(f for f in os.listdir(VOICE_DIR) if f.endswith(".onnx"))
    order = {vid: i for i, vid in enumerate(LABELS)}
    files.sort(key=lambda f: (order.get(f[:-5], len(order)), f))
    for f in files:
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
        syn = SynthesisConfig(speaker_id=SPEAKERS[vid]) if vid in SPEAKERS else None
        with lock:
            voice = get_voice(vid)
            with wave.open(buf, "wb") as w:
                voice.synthesize_wav(text, w, syn_config=syn)
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
