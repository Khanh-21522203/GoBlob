package security

import "testing"

func FuzzVerifyJWT(f *testing.F) {
	f.Add("")
	f.Add("invalid")
	f.Add("a.b.c")
	f.Add("Bearer token")

	const key = "test-signing-key"
	f.Fuzz(func(t *testing.T, token string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("VerifyJWT panicked: %v", r)
			}
		}()
		_, _ = VerifyJWT(token, key)
	})
}
