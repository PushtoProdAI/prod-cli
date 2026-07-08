# {{.Name}}

A [prod](https://github.com/PushtoProdAI/prod-cli) starter: a [Model Context Protocol](https://modelcontextprotocol.io)
server in TypeScript over streamable HTTP. prod detects the MCP SDK and deploys it as an
`mcp-server` — it's marked live only after prod completes an MCP `initialize` handshake, so a
plain web app that happens to return 200 won't be mistaken for a working server.

## Run locally

```bash
npm install
npm run build
npm start            # listens on :8080 (or $PORT)
```

## Deploy

```bash
prod "deploy this mcp server to fly"
```

## Make it yours

`src/index.ts` registers one demo `ping` tool. Add your own with `server.tool(name, description,
schema, handler)`. The transport is mounted at `/` (where the handshake lands) and is stateless —
a fresh transport per request — which keeps it simple to deploy and scale.

## Point an agent at it

Once deployed, add the live URL to your MCP client (Claude Code, Cursor, …) as an HTTP server, or
test the handshake with any MCP client.
