package httpapi

import (
	"time"

	"github.com/aisummoner/aisummoner/internal/device"
	"github.com/aisummoner/aisummoner/internal/store"
)

type userJSON struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func userResponse(user store.User) userJSON {
	return userJSON{ID: user.ID, Username: user.Username}
}

type deviceJSON struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Platform      string  `json:"platform"`
	Arch          string  `json:"arch"`
	ClientVersion string  `json:"client_version"`
	CreatedAt     string  `json:"created_at"`
	PairedAt      *string `json:"paired_at"`
	LastSeenAt    *string `json:"last_seen_at"`
	Online        bool    `json:"online"`
}

func deviceResponse(view device.View) deviceJSON {
	return deviceJSON{
		ID: view.Device.ID, Name: view.Device.Name, Platform: view.Device.Platform,
		Arch: view.Device.Arch, ClientVersion: view.Device.ClientVersion,
		CreatedAt: view.Device.CreatedAt.UTC().Format(time.RFC3339Nano),
		PairedAt:  formatOptionalTime(view.Device.PairedAt), LastSeenAt: formatOptionalTime(view.Device.LastSeenAt),
		Online: view.Online,
	}
}

func formatOptionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}
