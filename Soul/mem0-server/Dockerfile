FROM python:3.12-slim

WORKDIR /app

ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG ALL_PROXY

COPY requirements.txt .
RUN HTTP_PROXY=${HTTP_PROXY} HTTPS_PROXY=${HTTPS_PROXY} ALL_PROXY=${ALL_PROXY} pip install --no-cache-dir -r requirements.txt

COPY main.py .
RUN mkdir -p /app/history

EXPOSE 8000
ENV PYTHONUNBUFFERED=1
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
