//go:build !darwin && !linux

package sandbox

func kind() string { return "" }

func wrap([]string, []string) ([]string, error) { return nil, ErrUnavailable }

// RunHelper is the Linux re-exec entry point; unused here.
func RunHelper([]string) int { return 2 }
