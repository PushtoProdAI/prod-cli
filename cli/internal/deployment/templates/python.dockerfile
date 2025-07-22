FROM python:3.11-slim

WORKDIR /app

# Copy requirements
COPY requirements.txt .

# Install dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy application code
COPY . .

{{- if .BuildCommand }}
RUN {{ .BuildCommand }}
{{- end }}

# Expose port
EXPOSE {{ .Port }}

# Start the application
CMD [{{ .StartCommand }}]