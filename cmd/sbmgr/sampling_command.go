package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

const samplingCommandTimeout = 3 * time.Second
const samplingCommandOutputLimit = 8 * 1024 * 1024

var errSamplingOutputLimit = errors.New("统计命令输出超过单次读取上限；保留原采样位置等待重试")

type samplingOutput struct {
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
	over   bool
}

func (b *samplingOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.buffer.Len() {
		b.over = true
		b.cancel()
		return 0, errSamplingOutputLimit
	}
	return b.buffer.Write(data)
}

// Read-only sampling runs while accounting owns the state lock. A stalled
// child or inherited output pipe must not hold that lock indefinitely. Partial
// output is never returned as a successful counter or journal sample.
func runSamplingCommand(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &samplingOutput{limit: samplingCommandOutputLimit, cancel: cancel}
	stderr := &samplingOutput{limit: 64 * 1024, cancel: cancel}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.over || stderr.over {
		return nil, nil, errSamplingOutputLimit
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, stderr.buffer.Bytes(), err
	}
	return stdout.buffer.Bytes(), stderr.buffer.Bytes(), nil
}
