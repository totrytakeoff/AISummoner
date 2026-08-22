package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aisummoner/aisummoner/internal/store"
)

func TestBootstrapRequiresPasswordOnlyForFreshStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	persistence := &authStoreFake{}
	service := NewService(persistence)

	if _, _, err := service.Bootstrap(ctx, "", now); err == nil {
		t.Fatal("fresh bootstrap accepted an empty password")
	}
	if persistence.bootstrapCalls != 0 {
		t.Fatalf("fresh empty bootstrap wrote a user: calls=%d", persistence.bootstrapCalls)
	}

	createdUser, created, err := service.Bootstrap(ctx, "bootstrap-password", now)
	if err != nil || !created {
		t.Fatalf("fresh bootstrap: user=%#v created=%v err=%v", createdUser, created, err)
	}
	existingUser, created, err := service.Bootstrap(ctx, "", now.Add(time.Minute))
	if err != nil || created {
		t.Fatalf("second bootstrap: user=%#v created=%v err=%v", existingUser, created, err)
	}
	if existingUser.ID != createdUser.ID || persistence.bootstrapCalls != 1 {
		t.Fatalf("second bootstrap changed the admin: first=%#v second=%#v calls=%d", createdUser, existingUser, persistence.bootstrapCalls)
	}
}

func TestSessionLifecycleUsesOnlyTokenDigestInStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	passwordHash, err := hashPassword("correct-password", passwordParams{
		memory: 8 * 1024, iterations: 1, parallelism: 1, saltLength: 16, keyLength: 32,
	})
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	persistence := &authStoreFake{user: store.User{
		ID: "usr_test", Username: "admin", PasswordHash: passwordHash, CreatedAt: now,
	}}
	service := NewService(persistence)

	result, err := service.Login(ctx, "admin", "correct-password", now)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Token == "" {
		t.Fatal("Login returned an empty raw token")
	}
	wantDigest := DigestToken(result.Token)
	if !bytes.Equal(persistence.session.TokenDigest, wantDigest) {
		t.Fatalf("stored digest mismatch: got %x want %x", persistence.session.TokenDigest, wantDigest)
	}
	if bytes.Equal(persistence.session.TokenDigest, []byte(result.Token)) {
		t.Fatal("store received the raw session token")
	}

	authenticated, err := service.Authenticate(ctx, result.Token, now.Add(time.Minute))
	if err != nil || authenticated.ID != persistence.user.ID {
		t.Fatalf("Authenticate: user=%#v err=%v", authenticated, err)
	}
	if !bytes.Equal(persistence.lookupDigest, wantDigest) {
		t.Fatalf("authentication did not use token digest: got %x want %x", persistence.lookupDigest, wantDigest)
	}
	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if !bytes.Equal(persistence.deletedDigest, wantDigest) {
		t.Fatalf("logout did not use token digest: got %x want %x", persistence.deletedDigest, wantDigest)
	}
	if _, err := service.Authenticate(ctx, result.Token, now.Add(2*time.Minute)); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("logged out token authentication result: %v", err)
	}
}

func TestLoginVerificationAdmissionIsProcessGlobalAndRecovers(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	persistence := &authStoreFake{user: store.User{
		ID: "usr_test", Username: "admin", PasswordHash: "test-verifier-hash", CreatedAt: now,
	}}
	entered := make(chan struct{}, passwordVerificationCapacity+1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var loginWorkers sync.WaitGroup
	t.Cleanup(func() {
		unblock()
		loginWorkers.Wait()
	})

	var active atomic.Int32
	var maximum atomic.Int32
	verifier := func(string, string) (bool, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		entered <- struct{}{}
		<-release
		return false, nil
	}

	services := []*Service{NewService(persistence), NewService(persistence), NewService(persistence)}
	for _, service := range services {
		service.verifyPassword = verifier
	}
	results := make(chan error, passwordVerificationCapacity)
	loginWorkers.Add(passwordVerificationCapacity)
	for index := 0; index < passwordVerificationCapacity; index++ {
		service := services[index]
		go func() {
			defer loginWorkers.Done()
			_, err := service.Login(context.Background(), "admin", "wrong-password", now)
			results <- err
		}()
	}
	for index := 0; index < passwordVerificationCapacity; index++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("verification did not reach the admission barrier")
		}
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := services[2].Login(canceled, "admin", "wrong-password", now); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled login result: %v", err)
	}

	overload := make(chan error, 1)
	loginWorkers.Add(1)
	go func() {
		defer loginWorkers.Done()
		_, err := services[2].Login(context.Background(), "admin", "wrong-password", now)
		overload <- err
	}()
	select {
	case err := <-overload:
		if !errors.Is(err, ErrVerificationBusy) {
			t.Fatalf("overloaded login result: %v", err)
		}
	case <-time.After(time.Second):
		unblock()
		t.Fatal("overloaded login did not fail fast")
	}
	select {
	case <-entered:
		unblock()
		t.Fatal("overloaded or canceled login entered password verification")
	default:
	}

	unblock()
	for index := 0; index < passwordVerificationCapacity; index++ {
		select {
		case err := <-results:
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("admitted login result: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("admitted login did not finish after verifier release")
		}
	}
	if got := maximum.Load(); got != passwordVerificationCapacity {
		t.Fatalf("maximum concurrent verifications=%d want=%d", got, passwordVerificationCapacity)
	}
	if got := active.Load(); got != 0 {
		t.Fatalf("active verifications after release=%d", got)
	}

	sensitiveVerifierError := errors.New("sensitive verifier detail")
	services[2].verifyPassword = func(string, string) (bool, error) {
		return false, sensitiveVerifierError
	}
	if _, err := services[2].Login(context.Background(), "admin", "wrong-password", now); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("verifier error classification: %v", err)
	} else if strings.Contains(err.Error(), sensitiveVerifierError.Error()) {
		t.Fatalf("login exposed raw verifier error: %v", err)
	}

	services[2].verifyPassword = func(string, string) (bool, error) {
		panic("verifier panic sentinel")
	}
	recovered := false
	func() {
		defer func() {
			if recover() != nil {
				recovered = true
			}
		}()
		_, _ = services[2].Login(context.Background(), "admin", "wrong-password", now)
	}()
	if !recovered {
		t.Fatal("test verifier did not panic")
	}

	reentered := make(chan struct{}, 1)
	services[2].verifyPassword = func(string, string) (bool, error) {
		reentered <- struct{}{}
		return false, nil
	}
	if _, err := services[2].Login(context.Background(), "admin", "wrong-password", now); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login after capacity release: %v", err)
	}
	select {
	case <-reentered:
	default:
		t.Fatal("verification capacity did not recover after release")
	}
}

func TestLoginCancellationAfterLookupSkipsVerificationAndReleasesAdmission(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	lookupEntered := make(chan struct{})
	lookupRelease := make(chan struct{})
	var lookupReleaseOnce sync.Once
	releaseLookup := func() { lookupReleaseOnce.Do(func() { close(lookupRelease) }) }
	workerDone := make(chan struct{})
	t.Cleanup(func() {
		releaseLookup()
		<-workerDone
	})
	persistence := &blockingLookupStore{
		authStoreFake: authStoreFake{user: store.User{
			ID: "usr_test", Username: "admin", PasswordHash: "test-verifier-hash", CreatedAt: now,
		}},
		entered: lookupEntered,
		release: lookupRelease,
	}
	service := NewService(persistence)
	var verifierCalls atomic.Int32
	service.verifyPassword = func(string, string) (bool, error) {
		verifierCalls.Add(1)
		return false, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		defer close(workerDone)
		_, err := service.Login(ctx, "admin", "wrong-password", now)
		result <- err
	}()
	select {
	case <-lookupEntered:
	case <-time.After(time.Second):
		t.Fatal("login did not reach store lookup barrier")
	}
	cancel()
	releaseLookup()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled login result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled login did not return")
	}
	if got := verifierCalls.Load(); got != 0 {
		t.Fatalf("canceled login entered verification %d times", got)
	}

	persistence.passthrough = true
	if _, err := service.Login(context.Background(), "admin", "wrong-password", now); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("login after cancellation: %v", err)
	}
	if got := verifierCalls.Load(); got != 1 {
		t.Fatalf("admission did not recover after cancellation: verifier calls=%d", got)
	}
}

type authStoreFake struct {
	user           store.User
	session        store.WebSession
	bootstrapCalls int
	lookupDigest   []byte
	deletedDigest  []byte
}

type blockingLookupStore struct {
	authStoreFake
	entered     chan struct{}
	release     chan struct{}
	passthrough bool
}

func (fake *blockingLookupStore) UserByUsername(ctx context.Context, username string) (store.User, error) {
	if !fake.passthrough {
		close(fake.entered)
		<-fake.release
	}
	return fake.authStoreFake.UserByUsername(ctx, username)
}

func (fake *authStoreFake) HasUsers(context.Context) (bool, error) {
	return fake.user.ID != "", nil
}

func (fake *authStoreFake) BootstrapAdmin(_ context.Context, id, username, passwordHash string, now time.Time) (store.User, bool, error) {
	fake.bootstrapCalls++
	if fake.user.ID != "" {
		return fake.user, false, nil
	}
	fake.user = store.User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now}
	return fake.user, true, nil
}

func (fake *authStoreFake) UserByUsername(_ context.Context, username string) (store.User, error) {
	if fake.user.ID == "" || fake.user.Username != username {
		return store.User{}, store.ErrNotFound
	}
	return fake.user, nil
}

func (fake *authStoreFake) CreateWebSession(_ context.Context, session store.WebSession) error {
	fake.session = session
	return nil
}

func (fake *authStoreFake) UserBySessionDigest(_ context.Context, digest []byte, now time.Time) (store.User, error) {
	fake.lookupDigest = append([]byte(nil), digest...)
	if fake.user.ID == "" || fake.deletedDigest != nil || !bytes.Equal(digest, fake.session.TokenDigest) || !now.Before(fake.session.ExpiresAt) {
		return store.User{}, store.ErrNotFound
	}
	return fake.user, nil
}

func (fake *authStoreFake) DeleteWebSession(_ context.Context, digest []byte) error {
	fake.deletedDigest = append([]byte(nil), digest...)
	return nil
}
