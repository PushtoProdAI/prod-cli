# {{.Name}}

A [prod](https://github.com/PushtoProdAI/prod-cli) starter: a [Next.js](https://nextjs.org) app
(App Router, TypeScript). prod detects Next.js and deploys it as a `web` service.

## Run locally

```bash
npm install
npm run dev            # http://localhost:3000
```

## Deploy

```bash
prod "deploy this to vercel"
```

Vercel is the natural home for Next.js (it gets a preview URL per deploy), but prod can also ship
it to Netlify or, via a generated Dockerfile, to Fly / Cloud Run / App Runner.

## Make it yours

Edit `app/page.tsx`. Add routes as files under `app/`.
