package session

import (
	"fmt"
	"sync"

	"github.com/UserExistsError/conpty"
)

type ConPTYWrapper struct {
	cpty *conpty.ConPty
	mu   sync.Mutex
}

func StartClaude(workDir, binary string, env map[string]string) (*ConPTYWrapper, error) {
	opts := []conpty.ConPtyOption{
		conpty.ConPtyDimensions(120, 40),
	}

	if workDir != "" {
		opts = append(opts, conpty.ConPtyWorkDir(workDir))
	}

	if env != nil {
		var envList []string
		for k, v := range env {
			envList = append(envList, fmt.Sprintf("%s=%s", k, v))
		}
		opts = append(opts, conpty.ConPtyEnv(envList))
	}

	cpty, err := conpty.Start(binary, opts...)
	if err != nil {
		return nil, fmt.Errorf("conpty start: %w", err)
	}

	return &ConPTYWrapper{
		cpty: cpty,
	}, nil
}

func (w *ConPTYWrapper) Read(p []byte) (int, error) {
	return w.cpty.Read(p)
}

func (w *ConPTYWrapper) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cpty.Write(data)
}

func (w *ConPTYWrapper) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *ConPTYWrapper) Resize(cols, rows uint16) error {
	return w.cpty.Resize(int(cols), int(rows))
}

func (w *ConPTYWrapper) Close() error {
	return w.cpty.Close()
}

func (w *ConPTYWrapper) IsRunning() bool {
	return w.cpty != nil
}
