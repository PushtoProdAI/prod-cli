# Build stage
{{- if .BaseImage }}
FROM {{ .BaseImage }} AS builder
{{- else }}
FROM node:18-alpine AS builder
{{- end }}

WORKDIR /app

# Accept build arguments for environment variables (needed for import.meta.env)
# These ARGs are only available during build time and won't persist in the final image
{{- range .EnvVars }}
ARG {{ .Name }}
{{- end }}

# Copy package files
COPY package*.json ./

# Install all dependencies (including devDependencies for build)
RUN npm ci

# Copy source code
COPY . .

# Run build command if specified
{{- if .BuildCommand }}
# Create a temporary shell script that sets up environment variables from ARGs
# This ensures the variables are available to the build process but not stored in the image
RUN echo '#!/bin/sh' > /tmp/build.sh && \
{{- range .EnvVars }}
    echo 'export {{ .Name }}="${{ .Name }}"' >> /tmp/build.sh && \
{{- end }}
    echo '{{ .BuildCommand }}' >> /tmp/build.sh && \
    chmod +x /tmp/build.sh && \
    /tmp/build.sh && \
    rm /tmp/build.sh
{{- end }}

{{- if .IsStatic }}
# Production stage - serve with nginx
FROM nginx:alpine

WORKDIR /usr/share/nginx/html

# Copy built static files
COPY --from=builder /app/{{ .OutputDir }}/ ./

# Create nginx configuration for SPA
RUN echo 'server { \
    listen 80; \
    location / { \
        root /usr/share/nginx/html; \
        index index.html index.htm; \
        try_files $uri $uri/ /index.html; \
    } \
}' > /etc/nginx/conf.d/default.conf

# Expose port 80 for nginx
EXPOSE 80

# Start nginx
CMD ["nginx", "-g", "daemon off;"]
{{- else }}
# Production stage - Node.js runtime
{{- if .BaseImage }}
FROM {{ .BaseImage }} AS production
{{- else }}
FROM node:18-alpine AS production
{{- end }}

WORKDIR /app

# Copy all necessary runtime files first
COPY --from=builder /app .

# Remove the builder's node_modules (which has dev dependencies)
RUN rm -rf ./node_modules

# Install only production dependencies, skip prepare scripts
RUN npm ci --omit=dev --ignore-scripts && npm cache clean --force

# Expose port
EXPOSE {{ .Port }}

# Start the application
CMD {{ .StartCommand }}
{{- end }}