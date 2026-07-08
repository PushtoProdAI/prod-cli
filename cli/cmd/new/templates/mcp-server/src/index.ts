// A minimal Model Context Protocol server over streamable HTTP.
//
// prod detects the MCP SDK dependency and deploys this as an `mcp-server`: it's marked live
// only after prod completes an MCP `initialize` handshake against the root path, so the
// transport is mounted at "/". Replace the `ping` tool with your own.

import express from "express";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";

function buildServer(): McpServer {
  const server = new McpServer({ name: "{{.Name}}", version: "0.1.0" });

  server.tool(
    "ping",
    "Demo tool — replace me. Greets whoever you name.",
    { name: z.string().describe("who to greet") },
    async ({ name }) => ({
      content: [{ type: "text", text: `Hello, ${name}! Your MCP server is live.` }],
    }),
  );

  return server;
}

const app = express();
app.use(express.json());

// Stateless transport: a fresh transport per request keeps this simple to deploy and scale.
// The handshake and every tool call POST to "/", which is what prod's liveness check probes.
app.all("/", async (req, res) => {
  const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
  res.on("close", () => {
    void transport.close();
  });
  await buildServer().connect(transport);
  await transport.handleRequest(req, res, req.body);
});

const port = Number(process.env.PORT ?? 8080);
app.listen(port, () => {
  console.log(`MCP server listening on :${port}`);
});
