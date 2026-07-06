package registry

import "testing"

func TestACRLoginServer(t *testing.T) {
	a := &acrRegistry{registryName: "prodacr"}
	if got := a.loginServer(); got != "prodacr.azurecr.io" {
		t.Errorf("loginServer = %q, want prodacr.azurecr.io", got)
	}
}

func TestACRRef(t *testing.T) {
	a := &acrRegistry{registryName: "prodacr"}

	got, err := a.Ref("My-App", "1720000000")
	if err != nil {
		t.Fatalf("Ref: %v", err)
	}
	if want := "prodacr.azurecr.io/my-app:1720000000"; got != want {
		t.Errorf("Ref = %q, want %q", got, want)
	}

	if _, err := a.Ref("app", "bad tag!"); err == nil {
		t.Error("expected error for an invalid tag")
	}
}
