//go:build unix && !linux

package procstat

// defaultLister is ps on non-Linux Unixes; Linux reads /proc directly.
func defaultLister(all bool) ([]Proc, error) { return listPS(all) }
