# {{.Name}}

A [prod](https://github.com/PushtoProdAI/prod-cli) starter: a dependency-free Go HTTP API using
only the standard library's `net/http`. prod detects the `go.mod` and deploys it as a `web`
service. It compiles to a single static binary, so the container image is tiny.

## Run locally

```bash
go run .                # http://localhost:8080
```

## Deploy

```bash
prod "deploy this to fly"
```

prod builds a multi-stage image (compile → scratch/distroless) and runs the binary bound to
`$PORT`. Great fit for Fly, Cloud Run, and App Runner.

## Make it yours

Add handlers with `http.HandleFunc(...)` in `main.go`. Reach for a router/framework only when you
need one.
