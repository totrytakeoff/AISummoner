//go:build windows

// Package windowsprobe contains bounded Windows compatibility proofs used to
// freeze the Remote Client platform contracts before production enablement.
package windowsprobe

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mandatoryHighRID  = 0x00003000
	remoteDataVendor  = "AISummoner"
	remoteDataProduct = "RemoteClient"
)

// TokenFacts is the non-secret subset needed to prove the Windows equivalent
// of the Linux non-root contract.
type TokenFacts struct {
	UserSID      string
	LogonSID     string
	SessionID    uint32
	IntegrityRID uint32
	Elevated     bool
	System       bool
}

// LocalDataDirectory resolves the stable per-user Remote root through the
// Windows Known Folder API rather than trusting cwd or an environment string.
func LocalDataDirectory() (string, error) {
	root, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("resolve LocalAppData: %w", err)
	}
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("LocalAppData is not an absolute path")
	}
	return filepath.Join(root, remoteDataVendor, remoteDataProduct), nil
}

// CurrentTokenFacts reads all privilege facts from the exact current process
// token. Administrator group membership alone is deliberately not a failure.
func CurrentTokenFacts() (TokenFacts, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return TokenFacts{}, fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()

	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return TokenFacts{}, fmt.Errorf("read token user: %w", err)
	}
	userSID := user.User.Sid.String()
	logonSID, err := tokenLogonSID(token)
	if err != nil {
		return TokenFacts{}, err
	}
	integrity, err := tokenIntegrityRID(token)
	if err != nil {
		return TokenFacts{}, err
	}
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID); err != nil {
		return TokenFacts{}, fmt.Errorf("read process session: %w", err)
	}
	system, err := systemAccountSID(user.User.Sid)
	if err != nil {
		return TokenFacts{}, err
	}
	return TokenFacts{
		UserSID: userSID, LogonSID: logonSID, SessionID: sessionID,
		IntegrityRID: integrity, Elevated: token.IsElevated(), System: system,
	}, nil
}

// RequireOrdinaryInteractiveUser rejects the privilege contexts that would
// make Remote commands run with authority broader than the desktop user.
func RequireOrdinaryInteractiveUser() error {
	facts, err := CurrentTokenFacts()
	if err != nil {
		return err
	}
	return validateOrdinaryInteractiveUser(facts)
}

func validateOrdinaryInteractiveUser(facts TokenFacts) error {
	if facts.Elevated || facts.IntegrityRID >= mandatoryHighRID || facts.System || facts.SessionID == 0 {
		return errors.New("Windows Remote requires a non-elevated interactive user")
	}
	return nil
}

func currentLogonSID() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	return tokenLogonSID(token)
}

func tokenLogonSID(token windows.Token) (string, error) {
	groups, err := token.GetTokenGroups()
	if err != nil {
		return "", fmt.Errorf("read token groups: %w", err)
	}
	for _, group := range groups.AllGroups() {
		if group.Sid != nil && group.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			return group.Sid.String(), nil
		}
	}
	return "", errors.New("token has no logon SID")
}

func tokenIntegrityRID(token windows.Token) (uint32, error) {
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &size)
	if err != windows.ERROR_INSUFFICIENT_BUFFER || size == 0 {
		return 0, fmt.Errorf("size token integrity information: %w", err)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buffer[0], size, &size); err != nil {
		return 0, fmt.Errorf("read token integrity information: %w", err)
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	if label.Label.Sid == nil || label.Label.Sid.SubAuthorityCount() == 0 {
		return 0, errors.New("token integrity SID is invalid")
	}
	return label.Label.Sid.SubAuthority(uint32(label.Label.Sid.SubAuthorityCount() - 1)), nil
}

func systemAccountSID(actual *windows.SID) (bool, error) {
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid, windows.WinLocalServiceSid, windows.WinNetworkServiceSid,
	} {
		expected, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			return false, fmt.Errorf("create well-known account SID: %w", err)
		}
		if actual.Equals(expected) {
			return true, nil
		}
	}
	return false, nil
}

// ProtectPath replaces inherited filesystem permissions with a protected ACL
// granting full access only to the current user and LocalSystem. Directories
// propagate those entries to children created by the Remote Core.
func ProtectPath(path string, directory bool) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || user == nil || user.User.Sid == nil {
		return fmt.Errorf("read token user: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("create LocalSystem SID: %w", err)
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{
		fullAccessEntry(user.User.Sid, windows.TRUSTEE_IS_USER, inheritance),
		fullAccessEntry(system, windows.TRUSTEE_IS_WELL_KNOWN_GROUP, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("build protected ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		return fmt.Errorf("protect path ACL: %w", err)
	}
	return nil
}

func fullAccessEntry(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
