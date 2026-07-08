{{- if .BaseImage }}
FROM {{ .BaseImage }} AS builder
{{- else }}
FROM rust:1.82 AS builder
{{- end }}

WORKDIR /app

# Copy the whole project and build a release binary. The official `rust` image is
# buildpack-deps-based, so it already carries the C toolchain, pkg-config, and libssl-dev that
# native crates (openssl-sys, pq-sys, …) link against — no apt step needed.
COPY . .
RUN cargo build --release

# Move the built binary to a fixed path so the runtime stage needn't know its name. We expect
# Cargo to have produced `target/release/{{ .Name }}` (the [[bin]]/[package] name); if the app
# name and the Cargo binary name diverge, fall back to the first top-level executable under
# target/release.
RUN if [ -f "target/release/{{ .Name }}" ]; then \
        cp "target/release/{{ .Name }}" /app/server; \
    else \
        cp "$(find target/release -maxdepth 1 -type f -perm -u+x ! -name '*.d' | head -n1)" /app/server; \
    fi

# --- runtime stage ---
# distroless/cc carries glibc + libssl/libstdc++ for dynamically-linked Rust binaries, and
# nothing else — no shell, no package manager.
FROM gcr.io/distroless/cc-debian12

WORKDIR /app

COPY --from=builder /app/server /app/server

# The app is expected to read PORT (defaulting to {{ .Port }}) and bind 0.0.0.0.
ENV PORT={{ .Port }}
EXPOSE {{ .Port }}

CMD ["/app/server"]
