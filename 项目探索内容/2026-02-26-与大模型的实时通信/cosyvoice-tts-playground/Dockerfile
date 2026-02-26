FROM python:3.10-slim

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ENV HTTP_PROXY=${HTTP_PROXY} HTTPS_PROXY=${HTTPS_PROXY} ALL_PROXY=${ALL_PROXY}
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

WORKDIR /app

RUN apt-get update -o Acquire::Retries=5 && apt-get install -y --fix-missing --no-install-recommends \
    git git-lfs ffmpeg sox libsndfile1 libsox-dev build-essential \
    && rm -rf /var/lib/apt/lists/*

COPY CosyVoice /opt/CosyVoice
COPY cosyvoice-tts-playground/requirements-cpu.txt /app/requirements-cpu.txt

RUN pip install --no-cache-dir --upgrade pip "setuptools<81" wheel
RUN pip install --no-cache-dir --no-build-isolation openai-whisper==20231117
RUN pip install --no-cache-dir -r /app/requirements-cpu.txt

COPY cosyvoice-tts-playground/app /app/app
COPY cosyvoice-tts-playground/web /app/web

ENV COSYVOICE_REPO=/opt/CosyVoice
ENV COSY_MODEL_DIR=iic/CosyVoice-300M-SFT
ENV MODELSCOPE_CACHE=/models/modelscope
ENV HF_HOME=/models/huggingface

EXPOSE 18388
CMD ["uvicorn", "app.server:app", "--host", "0.0.0.0", "--port", "18388"]
