//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const (
	everyoneSID = "S-1-1-0"
	systemSID   = "S-1-5-18"
)

// inspect creates a named pipe through the same carrier creation path the
// library uses (winio.ListenPipe with a protected DACL for the current user
// and SYSTEM only, inheritance disabled), opens a second instance of that
// pipe, then reads the operating system security descriptor back off the live
// handle. It reports whether the DACL really restricts the pipe to those two
// identities.
func inspect(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	endpoint := flags.String("addr", "", "Windows named-pipe endpoint")
	_ = flags.String("dir", "", "certificate directory (keys peer fixture contract)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	path, err := singlePipePath(*endpoint)
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return errors.New("pipe: cannot resolve process identity for pipe ACL")
	}
	sid := user.User.Sid.String()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Mirror the pipe carrier exactly: the same wire endpoint and the same
	// protected DACL string the production listener installs, via winio.
	sddl := "D:P(A;;GA;;;" + sid + ")(A;;GA;;;SY)"
	listener, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: sddl,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		return fmt.Errorf("pipe: cannot create named pipe for ACL inspection: %w", err)
	}
	defer listener.Close()
	// Open a second instance of the existing pipe to obtain a live handle we
	// can query; the pipe object's security descriptor applies to every
	// instance, so this handle reflects the installed DACL.
	handle, err := windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1,
		64*1024,
		64*1024,
		0,
		nil,
	)
	if err != nil {
		return fmt.Errorf("pipe: cannot open pipe instance for ACL inspection: %w", err)
	}
	defer windows.CloseHandle(handle)
	readBack, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("pipe: cannot read pipe ACL back: %w", err)
	}
	control, _, err := readBack.Control()
	if err != nil {
		return fmt.Errorf("pipe: cannot read pipe DACL control bits: %w", err)
	}
	dacl, _, err := readBack.DACL()
	if err != nil {
		return fmt.Errorf("pipe: cannot read pipe DACL: %w", err)
	}
	var allowsUser, allowsSystem, allowsEveryone bool
	if dacl != nil {
		for index := uint32(0); index < uint32(dacl.AceCount); index++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, index, &ace); err != nil {
				continue
			}
			if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
				continue
			}
			aceSid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			switch aceSid.String() {
			case sid:
				allowsUser = true
			case systemSID:
				allowsSystem = true
			case everyoneSID:
				allowsEveryone = true
			}
		}
	}
	restricted := dacl != nil &&
		control&windows.SE_DACL_PROTECTED != 0 &&
		allowsUser &&
		allowsSystem &&
		!allowsEveryone
	emit(report{OK: true, DaclRestricted: restricted})
	return nil
}

func singlePipePath(endpoint string) (string, error) {
	const prefix = `\\.\pipe\`
	if len(endpoint) <= len(prefix) || !strings.EqualFold(endpoint[:len(prefix)], prefix) {
		return "", errors.New(`pipe: endpoint must have form \\.\pipe\name`)
	}
	name := endpoint[len(prefix):]
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || strings.TrimSpace(name) != name {
		return "", errors.New("pipe: endpoint must identify one named pipe")
	}
	return prefix + name, nil
}
