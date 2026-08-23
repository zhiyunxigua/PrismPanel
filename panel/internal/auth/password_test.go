package auth

import "testing"

func TestPasswordRoundTrip(t *testing.T) {
	t.Parallel()
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword(encoded, "incorrect password") {
		t.Fatal("unexpected password verification")
	}
}

func TestInvalidEncodedPassword(t *testing.T) {
	t.Parallel()
	if VerifyPassword("not-a-hash", "password") {
		t.Fatal("malformed hash must not verify")
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("short password must be rejected")
	}
	if err := ValidatePassword("123456"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := ValidatePassword("密码一二三四"); err != nil {
		t.Fatalf("unicode password rejected: %v", err)
	}
}
