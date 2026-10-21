package chat

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// NameOverrides holds display names assigned by the operator.
//
// The file is deliberately plain JSON in STATE_DIR so it can be edited by hand on
// the server without a rebuild or a restart: the server re-reads it when its
// modification time changes. Keys may be a full session id or its 5-character
// prefix (which is what the UI shows as `#abcde`).
//
//	{ "names": { "07444": "张三", "a1b2c3d4e5f60718...": "李四" } }
type NameOverrides struct {
	path string
	// interval is how often Lookup re-stats the file.
	interval time.Duration

	mu        sync.RWMutex
	bySession map[string]string
	byPrefix  map[string]string
	modTime   time.Time
	lastCheck time.Time
	// onChange is called with the affected keys after a reload changed them.
	onChange func(changed map[string]string)
}

// SetOnChange registers a callback invoked after the file changed. Called once at
// startup too, so nested locks are avoided by invoking it outside the mutex.
func (o *NameOverrides) SetOnChange(fn func(changed map[string]string)) {
	o.mu.Lock()
	o.onChange = fn
	o.mu.Unlock()
}

// namesFile is the file name inside STATE_DIR.
const namesFile = "names.json"

// defaultReloadInterval bounds how often the file is stat'ed; every WebSocket
// handshake calls Lookup, and a stat per connection is wasted work. Tests set it to
// zero to avoid sleeping.
const defaultReloadInterval = 2 * time.Second

type namesFileFormat struct {
	Names map[string]string `json:"names"`
}

// LoadNameOverrides reads the file if present. A missing file is not an error:
// names simply stay derived from the session id.
func LoadNameOverrides(dir string) *NameOverrides {
	o := &NameOverrides{
		interval:  defaultReloadInterval,
		bySession: map[string]string{},
		byPrefix:  map[string]string{},
	}

	if dir == "" {
		return o
	}

	o.path = filepath.Join(dir, namesFile)
	o.reload()

	if o.path != "" {
		if _, err := os.Stat(o.path); errors.Is(err, os.ErrNotExist) {
			// Create an empty template so the operator has something to edit.
			template := []byte("{\n  \"names\": {}\n}\n")

			if err := os.WriteFile(o.path, template, 0o600); err != nil {
				log.Printf("警告: 无法创建 %s: %v", o.path, err)
			}
		}
	}

	return o
}

// Lookup returns the operator-assigned name for a session, if any.
func (o *NameOverrides) Lookup(sessionID string) (string, bool) {
	o.maybeReload()

	o.mu.RLock()
	defer o.mu.RUnlock()

	if name, ok := o.bySession[sessionID]; ok {
		return name, true
	}

	prefix := sessionID
	if len(prefix) > 5 {
		prefix = prefix[:5]
	}

	name, ok := o.byPrefix[prefix]

	return name, ok
}

// maybeReload re-reads the file when it changed on disk.
func (o *NameOverrides) maybeReload() {
	if o.path == "" {
		return
	}

	now := time.Now()

	o.mu.RLock()
	recent := now.Sub(o.lastCheck) < o.interval
	o.mu.RUnlock()

	if recent {
		return
	}

	o.reload()
}

func (o *NameOverrides) reload() {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.lastCheck = time.Now()

	if o.path == "" {
		return
	}

	info, err := os.Stat(o.path)
	if err != nil {
		return
	}

	if !info.ModTime().After(o.modTime) {
		return
	}

	data, err := os.ReadFile(o.path)
	if err != nil {
		log.Printf("警告: 读取 %s 失败: %v", o.path, err)
		return
	}

	var parsed namesFileFormat
	if err := json.Unmarshal(data, &parsed); err != nil {
		// Keep the previous mapping: a half-saved file must not wipe every name.
		log.Printf("警告: %s 解析失败，保留上一版名字: %v", o.path, err)
		return
	}

	bySession := map[string]string{}
	byPrefix := map[string]string{}

	for key, name := range parsed.Names {
		key = strings.TrimSpace(key)
		name = strings.TrimSpace(name)

		if key == "" || name == "" {
			continue
		}

		if len(key) >= 32 {
			bySession[key] = name
		} else {
			byPrefix[key] = name
		}
	}

	changed := map[string]string{}

	for key, name := range byPrefix {
		if o.byPrefix[key] != name {
			changed[key] = name
		}
	}

	for key, name := range bySession {
		if o.bySession[key] != name {
			changed[key] = name
		}
	}

	o.bySession = bySession
	o.byPrefix = byPrefix
	o.modTime = info.ModTime()

	log.Printf("已加载 %d 条指定昵称（%s）", len(bySession)+len(byPrefix), o.path)

	if len(changed) > 0 && o.onChange != nil {
		go o.onChange(changed)
	}
}
