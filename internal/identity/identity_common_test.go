package identity

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

type memoryStorage struct {
	privateKey ed25519.PrivateKey
	metadata   *metadata
}

func (store *memoryStorage) LoadPrivateKey() (ed25519.PrivateKey, error) {
	if store.privateKey == nil {
		return nil, errPrivateKeyNotFound
	}
	return append(ed25519.PrivateKey(nil), store.privateKey...), nil
}

func (store *memoryStorage) CreatePrivateKey(value ed25519.PrivateKey) error {
	if store.privateKey != nil {
		return errors.New("private key already exists")
	}
	store.privateKey = append(ed25519.PrivateKey(nil), value...)
	return nil
}

func (store *memoryStorage) LoadMetadata() (*metadata, error) {
	if store.metadata == nil {
		return nil, errMetadataNotFound
	}
	value := *store.metadata
	return &value, nil
}

func (store *memoryStorage) WriteMetadata(value *metadata) error {
	copy := *value
	store.metadata = &copy
	return nil
}

func TestCommonIdentityLifecycleUsesPlatformStorage(t *testing.T) {
	store := &memoryStorage{}
	now := time.Date(2026, 8, 31, 7, 8, 9, 0, time.UTC)
	first, err := loadOrCreateFromStorage(store, bytes.NewReader(bytes.Repeat([]byte{4}, 64)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadOrCreateFromStorage(store, nil, func() time.Time { return now.Add(time.Hour) })
	if err != nil {
		t.Fatal(err)
	}
	if first.DeviceID != second.DeviceID || !second.CreatedAt.Equal(now) {
		t.Fatalf("identity changed across platform storage reload: first=%+v second=%+v", first, second)
	}
	store.metadata.DeviceID = "dev_wrong"
	if _, err := loadOrCreateFromStorage(store, nil, time.Now); err == nil {
		t.Fatal("metadata mismatch was accepted by common identity validation")
	}
}

func TestCommonIdentityFailsClosedWhenMetadataOutlivesKey(t *testing.T) {
	store := &memoryStorage{metadata: &metadata{
		Version: metadataVersion, DeviceID: "dev_orphaned", CreatedAt: time.Now().UTC(),
	}}
	if _, err := loadOrCreateFromStorage(store, bytes.NewReader(bytes.Repeat([]byte{8}, 64)), time.Now); err == nil {
		t.Fatal("orphaned metadata caused a silent identity replacement")
	}
	if store.privateKey != nil {
		t.Fatal("common identity lifecycle wrote a replacement key")
	}
}
