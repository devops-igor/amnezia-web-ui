# Stage 1: Build dependencies (contains build tools)
FROM python:3.13-slim AS builder

WORKDIR /app

# Install build tools needed for C extensions (cffi, bcrypt, cryptography)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libffi-dev \
    && rm -rf /var/lib/apt/lists/*

# Install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt


# Stage 2: Production (no build tools — lean image)
FROM python:3.13-slim

WORKDIR /app

# Copy installed site-packages from builder stage
COPY --from=builder /usr/local/lib/python3.13/site-packages/ /usr/local/lib/python3.13/site-packages/

# Copy pip entry-points / scripts from builder (gunicorn, uvicorn, etc.)
COPY --from=builder /usr/local/bin/ /usr/local/bin/

# Create non-root user for security
RUN addgroup --system appgroup && adduser --system --ingroup appgroup appuser

# Copy application code and assets
COPY *.py ./
COPY app/ ./app/
COPY schema.sql ./
COPY static/ ./static/
COPY templates/ ./templates/
COPY translations/ ./translations/
COPY protocol_telemt/ ./protocol_telemt/

# Create data directory and set ownership to non-root user
RUN mkdir -p /app/data && chown -R appuser:appgroup /app

# Expose the panel port (default 5000)
EXPOSE 5000

# Run as non-root user
USER appuser

# Run the application
CMD ["python", "app.py"]
