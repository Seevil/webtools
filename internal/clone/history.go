package clone

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type HistoryEntry struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Directory string    `json:"directory"`
	Entry     string    `json:"entry"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished"`
	Pages     int       `json:"pages"`
	Assets    int       `json:"assets"`
	Failed    int       `json:"failed"`
	Bytes     int64     `json:"bytes"`
	Status    string    `json:"status"`
	Resumable bool      `json:"resumable"`
}

const historyLimit = 200

func HistoryPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "SiteClone", "history.json"), nil
}

func LoadHistory(file string) ([]HistoryEntry, error) {
	info, err := os.Stat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 8*1024*1024 {
		return nil, errors.New("历史记录文件无效")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var list []HistoryEntry
	if err = json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func SaveHistory(file string, list []HistoryEntry) error {
	if len(list) > historyLimit {
		list = list[:historyLimit]
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// UpsertHistory puts the entry first, replacing an existing record with the same ID.
func UpsertHistory(list []HistoryEntry, entry HistoryEntry) []HistoryEntry {
	out := []HistoryEntry{entry}
	for _, h := range list {
		if h.ID != entry.ID {
			out = append(out, h)
		}
	}
	if len(out) > historyLimit {
		out = out[:historyLimit]
	}
	return out
}
