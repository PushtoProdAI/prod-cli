package agent

import "testing"

func TestShouldAutoRollback(t *testing.T) {
	cases := []struct {
		name             string
		supportsRollback bool
		isUpdate         bool
		want             bool
	}{
		{"cloud run update — revert", true, true, true},
		{"first-ever deploy — nothing to revert to", true, false, false},
		{"app runner (no rollback) update — report failed", false, true, false},
		{"no-rollback first deploy", false, false, false},
	}
	for _, c := range cases {
		if got := shouldAutoRollback(c.supportsRollback, c.isUpdate); got != c.want {
			t.Errorf("%s: shouldAutoRollback(%v,%v)=%v want %v", c.name, c.supportsRollback, c.isUpdate, got, c.want)
		}
	}
}
