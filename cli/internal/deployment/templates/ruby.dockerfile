{{- if .BaseImage }}
FROM {{ .BaseImage }} AS builder
{{- else }}
FROM ruby:3.3-slim AS builder
{{- end }}

WORKDIR /app

# Build-time system deps for native gems (pg, nokogiri, …) and asset compilation.
RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y \
        build-essential git libpq-dev libyaml-dev pkg-config && \
    rm -rf /var/lib/apt/lists/*

# Install gems first so the layer caches across code-only changes.
COPY Gemfile Gemfile.lock* ./
{{- if .BuildCommand }}
RUN {{ .BuildCommand }}
{{- else }}
RUN bundle install
{{- end }}

# Copy the application source.
COPY . .

{{- if .IsRails }}
# Precompile Rails assets. SECRET_KEY_BASE_DUMMY lets assets:precompile run without a real
# secret; only Rails apps reach this branch, so a Sinatra/plain-Rack app skips it entirely.
RUN SECRET_KEY_BASE_DUMMY=1 bundle exec rails assets:precompile
{{- end }}

# --- runtime stage ---
{{- if .BaseImage }}
FROM {{ .BaseImage }}
{{- else }}
FROM ruby:3.3-slim
{{- end }}

WORKDIR /app

# Runtime shared libraries for the native gems built above.
RUN apt-get update -qq && \
    apt-get install --no-install-recommends -y libpq5 libyaml-0-2 && \
    rm -rf /var/lib/apt/lists/*

# Bring over the installed gems and the (asset-compiled) application from the builder.
COPY --from=builder /usr/local/bundle /usr/local/bundle
COPY --from=builder /app /app

ENV RAILS_ENV=production \
    RACK_ENV=production \
    RAILS_LOG_TO_STDOUT=1 \
    RAILS_SERVE_STATIC_FILES=1 \
    PORT={{ .Port }}

EXPOSE {{ .Port }}

# Start the server.
{{- if .StartCommand }}
CMD {{ .StartCommand }}
{{- else }}
CMD bundle exec rails server -b 0.0.0.0 -p {{ .Port }}
{{- end }}
