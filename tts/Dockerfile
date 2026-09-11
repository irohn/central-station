# syntax=docker/dockerfile:1.7
# Text-to-speech service: Piper (offline neural TTS) behind a tiny HTTP API.
FROM python:3.12-slim
ARG PIPER_VOICE=en_US-lessac-medium
ENV PIPER_VOICE=$PIPER_VOICE PIPER_VOICE_DIR=/voices PYTHONUNBUFFERED=1
RUN pip install --no-cache-dir --root-user-action=ignore piper-tts==1.8.0 \
 && python -m piper.download_voices --download-dir /voices "$PIPER_VOICE"
COPY server.py /app/server.py
RUN useradd -r -u 1000 tts
USER 1000:1000
EXPOSE 5000
HEALTHCHECK --interval=30s --timeout=5s CMD python -c "import urllib.request;urllib.request.urlopen('http://127.0.0.1:5000/healthz')" || exit 1
CMD ["python", "/app/server.py"]
