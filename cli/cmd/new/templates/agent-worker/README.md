# {{.Name}}

A [prod](https://github.com/PushtoProdAI/prod-cli) starter: a [LangGraph](https://langchain-ai.github.io/langgraph/)
agent that runs as a background **worker** — no web server, no HTTP endpoint. prod detects the
agent framework and deploys it as a `worker`: a continuous process with no health check and no
public URL. It just runs.

## Run locally

```bash
python -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
cp .env.example .env      # add your OPENAI_API_KEY
python main.py
```

## Deploy

```bash
prod "deploy this worker to fly"
```

prod reads the project, shows you a plan (with cost), and — because this is a worker — skips the
HTTP health check that a portless process would fail. It'll prompt for `OPENAI_API_KEY` and set
it as a platform secret.

## Make it yours

`main.py` has a one-node graph and a `while True` loop with a `TODO`. Replace the loop body with
your real work source (a queue, a stream, a schedule) and grow the graph with tools and memory.
