package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost is deliberately above the library default (10). 12 is a reasonable
// 2020s baseline: ~250ms/hash, costly to brute-force, still fine for login rates.
const bcryptCost = 12

// HashPassword returns a bcrypt hash of the plaintext password. bcrypt salts
// internally, so equal passwords produce different hashes.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	return string(b), err
}

// CheckPassword reports whether plain matches the stored bcrypt hash. It runs in
// time independent of how far the bytes match (bcrypt's compare is constant-time).
func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}
