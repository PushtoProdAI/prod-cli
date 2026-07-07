package vercel

import "testing"

func TestHasDatabaseURL(t *testing.T) {
	if hasDatabaseURL([]string{"NODE_ENV=production", "DATABASE_URL="}) {
		t.Error("empty DATABASE_URL must count as no database URL (the Vercel case)")
	}
	if hasDatabaseURL([]string{"FOO=bar"}) {
		t.Error("no DATABASE_URL at all → false")
	}
	if !hasDatabaseURL([]string{"DATABASE_URL=postgres://u:p@host/db"}) {
		t.Error("a real DATABASE_URL → true")
	}
	if !hasDatabaseURL([]string{"POSTGRES_URL=postgres://x"}) {
		t.Error("POSTGRES_URL should count")
	}
}
