FROM python:3.11-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends \
    g++ \
    libglib2.0-0 \
    libgl1 \
    libgomp1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt /app/requirements.txt
RUN pip install --no-cache-dir -r /app/requirements.txt

COPY app /app/app
COPY web /app/web
COPY models /app/models
RUN mkdir -p /app/data

EXPOSE 18080

ENV PERSON_MODEL_PATH=/app/models/yolov8n.pt \
    DB_PATH=/app/data/identity.db \
    MAX_PEOPLE=5 \
    OCCLUSION_HOLD_MS=1500

CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "18080"]
