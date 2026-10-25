package signing

// SetWorkFactorForTesting lowers the scrypt cost of Encrypt so tests run
// quickly, and returns a function that restores the previous value.
func SetWorkFactorForTesting(n int) (restore func()) {
	prev := scryptWorkFactor
	scryptWorkFactor = n
	return func() { scryptWorkFactor = prev }
}
