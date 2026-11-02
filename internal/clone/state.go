package clone

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

const stateDir = ".siteclone"

type stateRecord struct {
	URL       string `json:"url"`
	Base      string `json:"base"`
	Local     string `json:"local"`
	Source    string `json:"source,omitempty"`
	Variant   string `json:"variant,omitempty"`
	Kind      string `json:"kind"`
	Hash      string `json:"hash"`
	LocalHash string `json:"localHash,omitempty"`
	Depth     int    `json:"depth,omitempty"`
	Page      bool   `json:"page"`
}

type stateItem struct {
	URL   string `json:"url"`
	Depth int    `json:"depth"`
	Page  bool   `json:"page"`
}

// taskState is everything needed to continue an unfinished task in the same directory.
type taskState struct {
	Version    int                    `json:"version"`
	Config     Config                 `json:"config"`
	Entry      string                 `json:"entry"`
	Bytes      int64                  `json:"bytes"`
	PageCount  int                    `json:"pageCount"`
	AssetCount int                    `json:"assetCount"`
	Skipped    int                    `json:"skipped"`
	Records    map[string]stateRecord `json:"records"`
	Pending    []stateItem            `json:"pending"`
	Seen       []string               `json:"seen"`
}

func statePath(root string) string { return filepath.Join(root, stateDir, "state.json") }

func saveState(root string, s taskState) error {
	s.Version = 1
	s.Config.Cookie = ""
	if err := os.MkdirAll(filepath.Join(root, stateDir), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := statePath(root) + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(root))
}

func loadState(root string) (taskState, error) {
	var s taskState
	file := statePath(root)
	info, err := os.Stat(file)
	if err != nil {
		return s, errors.New("该目录没有可续传的任务状态")
	}
	if !info.Mode().IsRegular() || info.Size() > 256*1024*1024 {
		return s, errors.New("任务状态文件无效")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || s.Records == nil {
		return s, errors.New("任务状态版本不受支持")
	}
	return s, nil
}

func (r stateRecord) restore() (saved, bool) {
	u, e1 := url.Parse(r.URL)
	b, e2 := url.Parse(r.Base)
	local := filepath.FromSlash(r.Local)
	if e1 != nil || e2 != nil || u.Scheme == "" || u.Host == "" || b.Scheme == "" || b.Host == "" || !filepath.IsLocal(local) || len(r.Hash) != 64 {
		return saved{}, false
	}
	source := filepath.FromSlash(r.Source)
	if r.Source != "" && !filepath.IsLocal(source) {
		return saved{}, false
	}
	var h [32]byte
	if _, err := hex.Decode(h[:], []byte(r.Hash)); err != nil {
		return saved{}, false
	}
	var localHash [32]byte
	if r.LocalHash != "" {
		if len(r.LocalHash) != 64 {
			return saved{}, false
		}
		if _, err := hex.Decode(localHash[:], []byte(r.LocalHash)); err != nil {
			return saved{}, false
		}
	}
	return saved{u: u, base: b, local: local, source: source, variant: r.Variant, kind: r.Kind, hash: h, localHash: localHash, page: r.Page, depth: r.Depth}, true
}

// CanResume reports whether a task directory has unfinished items.
func CanResume(root string) bool {
	s, err := loadState(root)
	return err == nil && len(s.Pending) > 0
}
