"""A minimal FastAPI service.

prod detects FastAPI (a web framework) and deploys this as a `web` service: it's marked live
after an HTTP probe. `/health` is a cheap liveness endpoint prod can target.
"""

import os

from fastapi import FastAPI

app = FastAPI(title="{{.Name}}")


@app.get("/")
def root() -> dict[str, str]:
    return {"message": "Your FastAPI service is live."}


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "8080")))
