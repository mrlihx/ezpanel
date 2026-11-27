package panelsdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
)

type StorageRequest struct {
	ChannelID string            `json:"channelId,omitempty"`
	Key       string            `json:"key,omitempty"`
	Values    map[string]string `json:"values"`
}

// BackupStorage is called by the panel when a backup channel is in use.
type BackupStorage interface {
	Test(context.Context, StorageRequest) error
	Upload(context.Context, StorageRequest, io.Reader) error
	Open(context.Context, StorageRequest) (io.ReadCloser, error)
	Delete(context.Context, StorageRequest) error
}

func (p *Plugin) OnBackupStorage(storage BackupStorage) error {
	return p.HandleFunc("/api/_panel/storage/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Panel-User") != "system" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		data, err := base64.RawURLEncoding.DecodeString(r.Header.Get("X-Panel-Storage-Request"))
		var input StorageRequest
		if err != nil || json.Unmarshal(data, &input) != nil {
			http.Error(w, "invalid storage request", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/api/_panel/storage/test":
			err = storage.Test(r.Context(), input)
		case "/api/_panel/storage/upload":
			err = storage.Upload(r.Context(), input, r.Body)
		case "/api/_panel/storage/open":
			var file io.ReadCloser
			file, err = storage.Open(r.Context(), input)
			if err == nil {
				defer file.Close()
				w.Header().Set("Content-Type", "application/octet-stream")
				if _, err := io.Copy(w, file); err != nil {
					panic(http.ErrAbortHandler)
				}
				return
			}
		case "/api/_panel/storage/delete":
			err = storage.Delete(r.Context(), input)
		default:
			http.NotFound(w, r)
			return
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "storage operation failed", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
