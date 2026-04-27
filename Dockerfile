# Amnezia Web Panel
FROM python:3.13-slim

WORKDIR /app

# Install system dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libffi-dev \
    && rm -rf /var/lib/apt/lists/*

# Copy and install Python dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

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
