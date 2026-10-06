//go:build windows

package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	everyoneSID = "S-1-1-0"
	systemSID   = "S-1-5-18"
)

// inspect creates a named pipe with the same protected DACL the pipe carrier
// installs (explicit access for the current user and SYSTEM only, DACL
// inheritance disabled) and reads the operating system security descriptor
// back off the live handle. It reports whether the DACL really restricts the
// pipe to those two identities.
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
	system, err := windows.StringToSid(systemSID)
	if err != nil {
		return errors.New("pipe: cannot resolve SYSTEM identity for pipe ACL")
	}
	accessEntries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
			},
		},
		{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(system),
			},
		},
	}
	owner := windows.TRUSTEE{
		TrusteeForm:  windows.TRUSTEE_IS_SID,
		TrusteeType:  windows.TRUSTEE_IS_USER,
		TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
	}
	descriptor, err := windows.BuildSecurityDescriptor(&owner, nil, accessEntries, nil, nil)
	if err != nil {
		return fmt.Errorf("pipe: cannot build pipe ACL: %w", err)
	}
	if err := descriptor.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return fmt.Errorf("pipe: cannot protect pipe DACL from inheritance: %w", err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1,
		64*1024,
		64*1024,
		0,
		attributes,
	)
	if err != nil {
		return fmt.Errorf("pipe: cannot create named pipe for ACL inspection: %w", err)
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
