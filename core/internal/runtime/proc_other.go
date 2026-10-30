//go:build !unix

package runtime

// groupAlive cannot probe process groups here; an orphaned job is
// treated as ended and everything it may have changed as uncertain.
func groupAlive(int) bool { return false }
