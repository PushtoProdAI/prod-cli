FROM node:18-alpine

WORKDIR /app

# Copy package files
COPY package*.json ./

# Install dependencies
RUN npm ci --only=production

# Copy application code
COPY . .

# Set build and start commands
{{- if .BuildCommand }}
RUN {{ .BuildCommand }}
{{- end }}

# Expose port
EXPOSE {{ .Port }}

# Start the application
CMD {{ .StartCommand }}