//go:build linux

package sandbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Landlock syscall numbers are shared by every architecture.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	landlockCreateRulesetVersion = 1
	landlockRulePathBeneath      = 1

	prSetNoNewPrivs = 38
	oPath           = 0x200000
)

const (
	accessWriteFile  = 1 << 1
	accessRemoveDir  = 1 << 4
	accessRemoveFile = 1 << 5
	accessMakeChar   = 1 << 6
	accessMakeDir    = 1 << 7
	accessMakeReg    = 1 << 8
	accessMakeSock   = 1 << 9
	accessMakeFifo   = 1 << 10
	accessMakeBlock  = 1 << 11
	accessMakeSym    = 1 << 12
	accessRefer      = 1 << 13 // ABI 2
	accessTruncate   = 1 << 14 // ABI 3

	writeRights = accessWriteFile | accessRemoveDir | accessRemoveFile | accessMakeChar |
		accessMakeDir | accessMakeReg | accessMakeSock | accessMakeFifo | accessMakeBlock | accessMakeSym
	fileRights = accessWriteFile | accessTruncate
)

var abiOnce = sync.OnceValue(func() int {
	v, _, errno := syscall.Syscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0
	}
	return int(v)
})

func kind() string {
	if abiOnce() < 1 {
		return ""
	}
	return "landlock"
}

func wrap(roots []string, argv []string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: cannot find agent-host: %w", err)
	}
	args := []string{self, HelperArg}
	for _, r := range roots {
		args = append(args, "--rw", r)
	}
	args = append(args, "--")
	return append(args, argv...), nil
}

// RunHelper applies Landlock to this process and execs the command. It
// only returns on failure, with the exit status to use.
func RunHelper(args []string) int {
	var roots []string
	i := 0
	for ; i < len(args); i++ {
		if args[i] == "--" {
			i++
			break
		}
		if args[i] == "--rw" && i+1 < len(args) {
			roots = append(roots, args[i+1])
			i++
		}
	}
	argv := args[i:]
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "paladin sandbox: no command")
		return 2
	}
	if err := restrict(roots); err != nil {
		fmt.Fprintf(os.Stderr, "paladin sandbox: %v\n", err)
		return 126
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "paladin sandbox: %v\n", err)
		return 127
	}
	err = syscall.Exec(path, argv, os.Environ())
	fmt.Fprintf(os.Stderr, "paladin sandbox: exec %s: %v\n", path, err)
	return 126
}

func restrict(roots []string) error {
	abi := abiOnce()
	if abi < 1 {
		return ErrUnavailable
	}
	handled := uint64(writeRights)
	file := uint64(accessWriteFile)
	if abi >= 2 {
		handled |= accessRefer
	}
	if abi >= 3 {
		handled |= accessTruncate
		file |= accessTruncate
	}
	attr := handled
	fd, _, errno := syscall.Syscall(sysLandlockCreateRuleset, uintptr(unsafe.Pointer(&attr)), 8, 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %v", errno)
	}
	defer syscall.Close(int(fd))

	paths := append([]string{"/dev/null", "/dev/zero", "/dev/tty", "/dev/pts", "/dev/shm"}, roots...)
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		allowed := handled
		if !st.IsDir() {
			allowed = file
		}
		if err := addPath(int(fd), p, allowed); err != nil {
			return err
		}
	}
	if _, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("prctl(NO_NEW_PRIVS): %v", errno)
	}
	if _, _, errno := syscall.Syscall(sysLandlockRestrictSelf, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %v", errno)
	}
	return nil
}

func addPath(ruleset int, path string, allowed uint64) error {
	pfd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer syscall.Close(pfd)
	// struct landlock_path_beneath_attr is packed: u64 + s32.
	var attr [12]byte
	binary.NativeEndian.PutUint64(attr[0:8], allowed)
	binary.NativeEndian.PutUint32(attr[8:12], uint32(int32(pfd)))
	_, _, errno := syscall.Syscall6(sysLandlockAddRule, uintptr(ruleset), landlockRulePathBeneath,
		uintptr(unsafe.Pointer(&attr[0])), 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("landlock_add_rule %s: %v", path, errno)
	}
	return nil
}
