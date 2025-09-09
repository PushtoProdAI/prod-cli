package analyzer

type BuildOutputCandidate struct {
	Path           string // e.g. "dist", "build", ".next"
	Source         string // which file led us here (e.g. "next.config.js", "tsconfig.json"). Can extend to be Makefile, pom.xml, etc
	Framework      string // detected framework name (e.g. "Next.js", "Vite")
	ConfigContents string // raw contents of the config file (for LLM context)
}

// TODO: extend to other languages, we'll probably need more to go here later
