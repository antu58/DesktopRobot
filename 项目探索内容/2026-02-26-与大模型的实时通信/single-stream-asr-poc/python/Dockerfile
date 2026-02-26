FROM python:3.12-slim

WORKDIR /app
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY
ENV HTTP_PROXY=${HTTP_PROXY} HTTPS_PROXY=${HTTPS_PROXY} ALL_PROXY=${ALL_PROXY}
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1

RUN apt-get update && apt-get install -y --no-install-recommends libatomic1 libgomp1 libsndfile1 && rm -rf /var/lib/apt/lists/*

COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY asr_bridge_funasr.py /app/asr_bridge_funasr.py

EXPOSE 2700
CMD ["uvicorn", "asr_bridge_funasr:app", "--host", "0.0.0.0", "--port", "2700"]
