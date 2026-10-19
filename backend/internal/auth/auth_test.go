package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	Iterations = 1000 // keep tests fast; production uses 600k
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery") {
		t.Error("valid password rejected")
	}
	if VerifyPassword(h, "correct horse batterY") {
		t.Error("wrong password accepted")
	}
	for _, bad := range []string{"", "md5$x", "pbkdf2_sha256$x$y$z", "pbkdf2_sha256$0$AA$AA"} {
		if VerifyPassword(bad, "x") {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

func TestPolicy(t *testing.T) {
	if CheckPolicy("short") == nil || CheckPolicy("long enough pass") != nil {
		t.Error("policy")
	}
}

func TestRoleRank(t *testing.T) {
	v, h, a := &User{Role: RoleViewer}, &User{Role: RoleHR}, &User{Role: RoleAdmin}
	if v.Can(RoleHR) || !h.Can(RoleHR) || h.Can(RoleAdmin) || !a.Can(RoleViewer) {
		t.Error("role ranking wrong")
	}
	var nilUser *User
	if nilUser.Can(RoleViewer) || (&User{Role: "root"}).Can(RoleViewer) {
		t.Error("unknown/nil users must have no rights")
	}
}
