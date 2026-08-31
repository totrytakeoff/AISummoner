//go:build windows

// Package winsecurity owns the Windows user-token and filesystem ACL
// primitives shared by the production Remote backends and their native probe.
package winsecurity

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const mandatoryHighRID = 0x00003000

// TokenFacts is the non-secret process-token state used by the Remote's
// ordinary interactive user policy.
type TokenFacts struct {
	UserSID      string
	LogonSID     string
	SessionID    uint32
	IntegrityRID uint32
	Elevated     bool
	System       bool
}

// LocalDataDirectory resolves a stable application directory through the
// Windows Known Folder API rather than cwd or an environment-only fallback.
func LocalDataDirectory(parts ...string) (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("open process token for LocalAppData: %w", err)
	}
	defer token.Close()

	root, knownFolderErr := token.KnownFolderPath(
		windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT,
	)
	if knownFolderErr != nil {
		// CreateProcessWithLogonW can supply an inherited environment block even
		// when the alternate user's profile is loaded. Some Shell configurations
		// then reject the implicit/current-user Known Folder lookup. Resolve the
		// profile from the explicit process token so neither LOCALAPPDATA nor
		// USERPROFILE can redirect private Device Identity storage.
		profile, profileErr := token.GetUserProfileDirectory()
		if profileErr != nil {
			return "", fmt.Errorf("resolve LocalAppData from current token (known folder: %v; profile: %w)",
				knownFolderErr, profileErr)
		}
		root = filepath.Join(profile, "AppData", "Local")
	}
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("LocalAppData is not an absolute path")
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

// CurrentTokenFacts reads the exact current process token. Membership in the
// Administrators group is deliberately not treated as elevation.
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
	logonSID, err := TokenLogonSID(token)
	if err != nil {
		return TokenFacts{}, err
	}
	integrityRID, err := tokenIntegrityRID(token)
	if err != nil {
		return TokenFacts{}, err
	}
	var sessionID uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &sessionID); err != nil {
		return TokenFacts{}, fmt.Errorf("read process session: %w", err)
	}
	system, err := SystemAccountSID(user.User.Sid)
	if err != nil {
		return TokenFacts{}, err
	}
	return TokenFacts{
		UserSID: user.User.Sid.String(), LogonSID: logonSID, SessionID: sessionID,
		IntegrityRID: integrityRID, Elevated: token.IsElevated(), System: system,
	}, nil
}

// ValidateOrdinaryInteractiveUser rejects privilege contexts that would make
// Remote commands run with broader authority than the desktop user.
func ValidateOrdinaryInteractiveUser(facts TokenFacts) error {
	if facts.Elevated || facts.IntegrityRID >= mandatoryHighRID || facts.System || facts.SessionID == 0 {
		return errors.New("Windows Remote requires a non-elevated interactive user")
	}
	return nil
}

func RequireOrdinaryInteractiveUser() error {
	facts, err := CurrentTokenFacts()
	if err != nil {
		return err
	}
	return ValidateOrdinaryInteractiveUser(facts)
}

func CurrentLogonSID() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	return TokenLogonSID(token)
}

func TokenLogonSID(token windows.Token) (string, error) {
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

func SystemAccountSID(actual *windows.SID) (bool, error) {
	if actual == nil {
		return false, errors.New("token user SID is unavailable")
	}
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

// ProtectPath replaces inherited permissions with a protected DACL granting
// full access only to the current user and LocalSystem. Directory entries
// propagate to newly created descendants.
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

func RequireDirectory(path string) error {
	attributes, err := pathAttributes(path)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("Windows data path must be a non-reparse directory")
	}
	return nil
}

func RequireRegularFile(path string) error {
	attributes, err := pathAttributes(path)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("Windows data file must be a non-reparse regular file")
	}
	return nil
}

func pathAttributes(path string) (uint32, error) {
	encoded, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, errors.New("Windows path is invalid")
	}
	attributes, err := windows.GetFileAttributes(encoded)
	if err != nil {
		return 0, fmt.Errorf("inspect Windows path: %w", err)
	}
	return attributes, nil
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
