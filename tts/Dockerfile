# syntax=docker/dockerfile:1.7
# Text-to-speech service: Piper (offline neural TTS) behind a tiny HTTP API.
# PIPER_VOICES lists the voices baked into the image (~60-140 MB each, ~1 GB
# in total). Labels and speaker choices live in server.py.
FROM python:3.12-slim
ARG PIPER_VOICES="en_US-norman-medium en_US-amy-medium en_US-hfc_female-medium en_US-hfc_male-medium en_US-joe-medium en_US-kristin-medium en_US-l2arctic-medium en_US-lessac-high en_US-libritts-high en_US-libritts_r-medium en_US-mike-medium en_US-ryan-high en_GB-jenny_dioco-medium en_GB-alan-medium"
ENV PIPER_VOICE_DIR=/voices PIPER_DEFAULT_VOICE=en_US-norman-medium PYTHONUNBUFFERED=1
RUN pip install --no-cache-dir --root-user-action=ignore piper-tts==1.8.0 \
 && python -m piper.download_voices --download-dir /voices $PIPER_VOICES \
 && ls -la /voices
COPY server.py /app/server.py
RUN useradd -r -u 1000 tts
USER 1000:1000
EXPOSE 5000
HEALTHCHECK --interval=30s --timeout=5s CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:5000/healthz')" || exit 1
CMD ["python", "/app/server.py"]
