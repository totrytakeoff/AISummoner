package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	params := passwordParams{memory: 8 * 1024, iterations: 1, parallelism: 1, saltLength: 16, keyLength: 32}
	hash, err := hashPassword("correct horse", params)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	valid, err := VerifyPassword(hash, "correct horse")
	if err != nil || !valid {
		t.Fatalf("correct password rejected: valid=%v err=%v", valid, err)
	}
	valid, err = VerifyPassword(hash, "wrong")
	if err != nil {
		t.Fatalf("verify wrong password: %v", err)
	}
	if valid {
		t.Fatal("wrong password accepted")
	}
}

func TestDefaultPasswordParametersMatchBaseline(t *testing.T) {
	if defaultPasswordParams.memory != 64*1024 || defaultPasswordParams.iterations != 3 ||
		defaultPasswordParams.parallelism != 2 || defaultPasswordParams.saltLength != 16 || defaultPasswordParams.keyLength != 32 {
		t.Fatalf("unexpected default parameters: %#v", defaultPasswordParams)
	}
}

func TestVerifyRejectsUnsafeHashParameters(t *testing.T) {
	unsafe := "$argon2id$v=19$m=999999,t=3,p=2$c2FsdHNhbHRzYWx0c2FsdA$MDEyMzQ1Njc4OWFiY2RlZg"
	if _, err := VerifyPassword(unsafe, "password"); err == nil {
		t.Fatal("unsafe hash parameters accepted")
	}
}
