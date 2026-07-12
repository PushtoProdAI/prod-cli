{{- if .BaseImage }}
FROM {{ .BaseImage }} AS builder
{{- else }}
FROM mcr.microsoft.com/dotnet/sdk:9.0 AS builder
{{- end }}

WORKDIR /src

# Copy the whole project, restore, then publish a framework-dependent Release build to /app/publish.
# `dotnet publish` restores implicitly; running restore first keeps that step as its own cache layer.
COPY . .
RUN dotnet restore
RUN dotnet publish -c Release -o /app/publish

# --- runtime stage ---
# Chiseled, non-root ASP.NET runtime: a distroless-style image with NO shell and NO package manager.
# Because there's no shell, the final stage can only use COPY/ENV/EXPOSE/USER/ENTRYPOINT — never RUN
# — and there's no way to `find` the entrypoint DLL at runtime, so we rely on the deterministic
# assembly name the analyzer derived from <AssemblyName> or the .csproj filename.
FROM mcr.microsoft.com/dotnet/aspnet:9.0-noble-chiseled

WORKDIR /app
COPY --from=builder /app/publish .

# .NET 8+ reads ASPNETCORE_HTTP_PORTS to choose Kestrel's listen port (defaults to 8080).
ENV ASPNETCORE_HTTP_PORTS={{ .Port }}
EXPOSE {{ .Port }}

# The published assembly is <AssemblyName>.dll (== spec.Name). The chiseled base already runs as the
# non-root `app` user.
ENTRYPOINT ["dotnet", "{{ .Name }}.dll"]
