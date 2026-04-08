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

# Copy application code and assets
COPY *.py ./
COPY static/ ./static/
COPY templates/ ./templates/
COPY translations/ ./translations/

# Expose the panel port (default 5000)
EXPOSE 5000

# Run the application
CMD ["python", "app.py"]
