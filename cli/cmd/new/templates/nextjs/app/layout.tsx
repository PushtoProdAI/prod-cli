export const metadata = {
  title: "{{.Name}}",
  description: "A Next.js app deployed with prod.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
