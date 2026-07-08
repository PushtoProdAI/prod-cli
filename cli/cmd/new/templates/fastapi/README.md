# {{.Name}}

A [prod](https://github.com/PushtoProdAI/prod-cli) starter: a [FastAPI](https://fastapi.tiangolo.com)
service. prod detects FastAPI and deploys it as a `web` service (HTTP liveness), targeting
`/health`.

## Run locally

```bash
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
uvicorn main:app --reload      # http://localhost:8000
```

## Deploy

```bash
prod "deploy this to fly"
```

prod generates a Dockerfile and runs `uvicorn main:app` bound to `$PORT`. Works on Fly, Render,
Cloud Run, App Runner, and Azure Container Apps.

## Make it yours

Add routes with `@app.get(...)` / `@app.post(...)` in `main.py`.
