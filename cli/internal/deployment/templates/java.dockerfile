{{- if .BaseImage }}
FROM {{ .BaseImage }} AS builder
{{- else }}
FROM eclipse-temurin:21-jdk AS builder
{{- end }}

WORKDIR /app

# Copy the whole project and build the runnable jar with the detected build command
# (Maven `package` or Gradle `bootJar`). The wrapper scripts (mvnw/gradlew) are copied along with
# the source; make them executable in case the host lost the bit.
COPY . .
RUN chmod +x mvnw gradlew 2>/dev/null || true
RUN {{ .BuildCommand }}

# Locate the fat jar and copy it to a fixed path so the runtime stage needn't know its exact name.
# Maven writes it to target/, Gradle's bootJar to build/libs/. Gradle ALSO emits a `*-plain.jar`
# (the thin, non-executable jar) which we exclude so we run the bootable one.
RUN JAR=$(find target build/libs -maxdepth 1 -name '*.jar' ! -name '*-plain.jar' 2>/dev/null | head -n1); \
    if [ -z "$JAR" ]; then echo "no runnable jar found under target/ or build/libs/" >&2; exit 1; fi; \
    cp "$JAR" /app/app.jar

# --- runtime stage ---
FROM eclipse-temurin:21-jre

WORKDIR /app

COPY --from=builder /app/app.jar /app/app.jar

# Spring Boot reads SERVER_PORT; other frameworks commonly read PORT. Set both to the target port
# so the app binds where the platform expects regardless of framework.
ENV PORT={{ .Port }}
ENV SERVER_PORT={{ .Port }}
EXPOSE {{ .Port }}

CMD ["java", "-jar", "/app/app.jar"]
