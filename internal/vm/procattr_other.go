//go:build !linux

package vm

import "syscall"

func DaemonChildProcAttr() *syscall.SysProcAttr {
	return nil
}
