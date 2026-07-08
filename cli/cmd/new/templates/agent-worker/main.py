"""A minimal LangGraph agent that runs as a background worker.

There is no web server here. prod detects the agent framework (langgraph) with no web
framework and deploys this as a `worker` shape: a continuous process with no HTTP health
check and no public URL. Replace the loop body with your real work source — polling a
queue, consuming a stream, or running on a schedule.
"""

import os
import time

from langchain_openai import ChatOpenAI
from langgraph.graph import END, START, StateGraph
from typing_extensions import TypedDict


class State(TypedDict):
    task: str
    result: str


def run_agent(state: State) -> State:
    llm = ChatOpenAI(model="gpt-4o-mini")
    reply = llm.invoke(state["task"])
    return {"task": state["task"], "result": reply.content}


# A one-node graph. Grow this into your real agent (tools, memory, multiple nodes).
_graph = StateGraph(State)
_graph.add_node("agent", run_agent)
_graph.add_edge(START, "agent")
_graph.add_edge("agent", END)
agent = _graph.compile()


def main() -> None:
    if not os.environ.get("OPENAI_API_KEY"):
        raise SystemExit("OPENAI_API_KEY is not set — copy .env.example to .env and fill it in")

    print("agent-worker started", flush=True)
    while True:
        # TODO: replace this with your real work (pull a job, read a message, etc.).
        out = agent.invoke({"task": "Give a one-sentence status update.", "result": ""})
        print(out["result"], flush=True)
        time.sleep(60)


if __name__ == "__main__":
    main()
