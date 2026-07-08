// Styles live in an object rather than a JSX inline style so this scaffold template avoids
// double-brace syntax (it would collide with the scaffolder's name substitution). The
// scaffolded copy is plain TSX — edit it freely.
const wrap = {
  fontFamily: "system-ui",
  maxWidth: 640,
  margin: "4rem auto",
  padding: "0 1rem",
};

export default function Home() {
  return (
    <main style={wrap}>
      <h1>{{.Name}}</h1>
      <p>
        Your Next.js app is live. Edit <code>app/page.tsx</code> and redeploy.
      </p>
    </main>
  );
}
