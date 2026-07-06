# prod on Windows

Use **WSL2**. prod is a native (CGO) binary — it ships for macOS and Linux, and the Linux
build runs unchanged inside WSL2. Native Windows is deferred (BAML's CGO/mingw toolchain).

## Setup

1. Install WSL2 with a Linux distribution (e.g. Ubuntu) — from the Microsoft Store, or:
   ```powershell
   wsl --install
   ```
2. Open your WSL2 shell (e.g. "Ubuntu") and install prod with the normal one-liner — it
   detects Linux and installs the Linux build:
   ```bash
   curl -fsSL https://raw.githubusercontent.com/pushtoprodai/prod-cli/main/scripts/install.sh | sh
   ```
3. Check your setup, then deploy:
   ```bash
   prod doctor
   prod "deploy this to fly"
   ```

Everything works as on Linux — the cloud credentials, Docker (Docker Desktop's WSL2
integration or Docker inside the distro), and the MCP server all run in your WSL2 shell.

> Running the one-liner in native PowerShell/cmd will refuse with a pointer back here.
