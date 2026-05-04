package main

import "syscall"

// detachedSysProcAttr returns a SysProcAttr that creates the process completely
// outside the Windows service's Job Object.
// CREATE_BREAKAWAY_FROM_JOB (0x01000000) — escapes the job so the batch file
//   is not killed when the service stops itself (SCM sets BREAKAWAY_OK).
// CREATE_NEW_PROCESS_GROUP (0x00000200) — isolates signal handling.
// CREATE_NO_WINDOW      (0x08000000) — no console window (cmd runs headless).
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x01000000 | 0x00000200 | 0x08000000,
	}
}
