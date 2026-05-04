package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// serviceName is the canonical Windows service name used for install, uninstall, and restart.
const serviceName = "Calendarr"

// calendarrService implements the Windows service Execute interface.
type calendarrService struct{}

func (s *calendarrService) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	// Start the app in a goroutine so we can signal Running immediately.
	go startApp()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for c := range r {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		}
	}
	return false, 0
}

// runIfWindowsService checks whether we were launched by the Windows SCM.
// If so, hands control to svc.Run and returns true. Returns false otherwise.
func runIfWindowsService() bool {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false
	}
	if err := svc.Run(serviceName, &calendarrService{}); err != nil {
		fmt.Fprintln(os.Stderr, "Service error:", err)
		os.Exit(1)
	}
	return true
}

// installService registers calendarr.exe as an auto-start Windows service.
// exePath must be the absolute path to the executable.
// dataDirPath is passed as --data to the service command line (empty = omit flag).
func installService(exePath, dataDirPath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to service manager: %w", err)
	}
	defer m.Disconnect()

	// Reject if already installed.
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists — run --uninstall first", serviceName)
	}

	var args []string
	if dataDirPath != "" {
		args = append(args, "--data", dataDirPath)
	}

	s, err := m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName: "Calendarr",
		Description: "Calendarr — Radarr & Sonarr to Google Calendar sync",
		StartType:   mgr.StartAutomatic,
	}, args...)
	if err != nil {
		return fmt.Errorf("cannot create service: %w", err)
	}
	s.Close()
	return nil
}

// uninstallService removes the Calendarr Windows service registration.
func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()
	return s.Delete()
}
