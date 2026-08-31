//go:build windows

// Package windowsprobe contains bounded Windows compatibility proofs used to
// freeze the Remote Client platform contracts before production enablement.
package windowsprobe

import (
	"github.com/aisummoner/aisummoner/internal/winsecurity"
	"golang.org/x/sys/windows"
)

const (
	mandatoryHighRID  = 0x00003000
	remoteDataVendor  = "AISummoner"
	remoteDataProduct = "RemoteClient"
)

type TokenFacts = winsecurity.TokenFacts

func LocalDataDirectory() (string, error) {
	return winsecurity.LocalDataDirectory(remoteDataVendor, remoteDataProduct)
}

func CurrentTokenFacts() (TokenFacts, error) {
	return winsecurity.CurrentTokenFacts()
}

func RequireOrdinaryInteractiveUser() error {
	return winsecurity.RequireOrdinaryInteractiveUser()
}

func validateOrdinaryInteractiveUser(facts TokenFacts) error {
	return winsecurity.ValidateOrdinaryInteractiveUser(facts)
}

func currentLogonSID() (string, error) {
	return winsecurity.CurrentLogonSID()
}

func tokenLogonSID(token windows.Token) (string, error) {
	return winsecurity.TokenLogonSID(token)
}

func systemAccountSID(actual *windows.SID) (bool, error) {
	return winsecurity.SystemAccountSID(actual)
}

func ProtectPath(path string, directory bool) error {
	return winsecurity.ProtectPath(path, directory)
}
