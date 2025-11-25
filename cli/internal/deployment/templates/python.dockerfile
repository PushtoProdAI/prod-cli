{{- if .BaseImage }}
FROM {{ .BaseImage }}
{{- else }}
FROM python:3.11-slim
{{- end }}

WORKDIR /app

# Copy dependency files based on project type
{{- if .BuildCommand }}
{{-   if contains .BuildCommand "poetry" }}
COPY pyproject.toml poetry.lock* ./
{{-   else if contains .BuildCommand "pipenv" }}
COPY Pipfile Pipfile.lock* ./
{{-   else if contains .BuildCommand "requirements.txt" }}
COPY requirements.txt .
{{-   else if or (contains .BuildCommand "hatch") (contains .BuildCommand "pip install -e") }}
# Hatch projects need full source code for version detection
COPY . .
{{-   else if contains .BuildCommand "pip install" }}
COPY pyproject.toml setup.py* setup.cfg* ./
{{-   else }}
COPY requirements.txt .
{{-   end }}
{{- else }}
COPY requirements.txt .
{{- end }}

# Install package manager and dependencies
{{- if .BuildCommand }}
{{-   if contains .BuildCommand "poetry" }}
RUN pip install --no-cache-dir poetry>=1.4.0
RUN poetry config virtualenvs.create false
RUN {{ .BuildCommand }}
{{-   else if contains .BuildCommand "pipenv" }}
RUN pip install --no-cache-dir pipenv && {{ .BuildCommand }}
{{-   else if contains .BuildCommand "pip-sync" }}
RUN pip install --no-cache-dir pip-tools && {{ .BuildCommand }}
{{-   else if contains .BuildCommand "pip-compile" }}
RUN pip install --no-cache-dir pip-tools && {{ .BuildCommand }}
{{-   else if contains .BuildCommand "pdm" }}
RUN pip install --no-cache-dir pdm && {{ .BuildCommand }}
{{-   else if or (contains .BuildCommand "hatch") (contains .BuildCommand "pip install -e") }}
RUN pip install --no-cache-dir hatch hatchling && {{ .BuildCommand }}
{{-   else }}
RUN {{ .BuildCommand }}
{{-   end }}
{{- else }}
RUN pip install --no-cache-dir -r requirements.txt
{{- end }}

# Copy application code (skip if already copied for hatch)
{{- if .BuildCommand }}
{{-   if not (or (contains .BuildCommand "hatch") (contains .BuildCommand "pip install -e")) }}
COPY . .
{{-   end }}
{{- else }}
COPY . .
{{- end }}

# Collect static files for Django if needed
{{- if .IsDjango }}
RUN python manage.py collectstatic --noinput --clear || echo "No static files to collect"
{{- end }}

# Set environment variables for production deployment
ENV HOST=0.0.0.0
ENV PORT=8000
ENV FLASK_RUN_HOST=0.0.0.0
ENV FLASK_RUN_PORT=8000

# Copy and setup startup script for Python server handling
{{- if .HasStartupScript }}
COPY start.sh /app/start.sh
RUN chmod +x /app/start.sh
{{- end }}

# Expose port  
EXPOSE {{ .Port }}

# Start the application
{{- if .HasStartupScript }}
{{-   if .StartCommand }}
CMD /app/start.sh {{ .StartCommand }}
{{-   else }}
CMD /app/start.sh python app.py
{{-   end }}
{{- else }}
{{-   if .StartCommand }}
CMD {{ .StartCommand }}
{{-   else }}
CMD python app.py
{{-   end }}
{{- end }}
