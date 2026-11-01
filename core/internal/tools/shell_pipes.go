package tools

import (
	"io"
	"os"
	"os/exec"
)

// startPiped starts cmd with its stdout and stderr on pipes the caller
// owns. exec's StdoutPipe would be closed by Wait as soon as the process
// exits, dropping output still buffered in the pipe when Wait runs
// alongside the readers (as the shell tool does); these stay open until
// the caller has read to EOF and closes them.
func startPiped(cmd *exec.Cmd) (io.ReadCloser, io.ReadCloser, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return nil, nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	err = cmd.Start()
	// The child has its own copies; ours must go so EOF arrives.
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return nil, nil, err
	}
	return outR, errR, nil
}
