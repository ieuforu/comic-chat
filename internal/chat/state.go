package chat

import (
	"crypto/rand"
	"log"
	"os"
	"path/filepath"
)

// State directory: everything that has to survive a restart lives here. Keeping it
// in one place makes it obvious what to back up (and what to keep out of git).
const (
	stateDirEnv     = "STATE_DIR"
	defaultStateDir = ".state"
	secretFile      = "session-secret"
)

// stateDirectory resolves (and creates) the directory used for persisted state.
//
// Order: STATE_DIR, then ./.state, then .state next to the executable. The last
// fallback matters under systemd, where the working directory is often / and a
// relative path cannot be created — silently losing the signing key and handing
// every user a new identity on each restart.
func StateDirectory() string {
	candidates := []string{}

	if explicit := os.Getenv(stateDirEnv); explicit != "" {
		candidates = append(candidates, explicit)
	} else {
		candidates = append(candidates, defaultStateDir)

		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), defaultStateDir))
		}
	}

	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			return dir
		}
	}

	log.Printf("警告: 无法创建状态目录 %v（身份将无法跨重启保持，请设置 STATE_DIR）", candidates)

	return ""
}

// loadOrCreateSecret returns the cookie signing key, creating and persisting one on
// first run.
//
// Without this, a restart with no SESSION_SECRET invalidates every issued cookie,
// so every user silently becomes a new person — which is exactly the behaviour a
// stable identity must not have. Order of precedence:
//
//	SESSION_SECRET env > state dir file > random (last resort, logged loudly)
func loadOrCreateSecret(dir string) []byte {
	if secret := os.Getenv("SESSION_SECRET"); secret != "" {
		return []byte(secret)
	}

	if dir == "" {
		log.Println("警告: 没有状态目录也没有 SESSION_SECRET，身份会在重启后重置")

		return randomBytes(32)
	}

	path := filepath.Join(dir, secretFile)

	if data, err := os.ReadFile(path); err == nil && len(data) >= 32 {
		return data
	}

	secret := randomBytes(32)

	if err := os.WriteFile(path, secret, 0o600); err != nil {
		log.Printf("警告: 无法持久化会话密钥 %s: %v（身份将在重启后重置）", path, err)
		return secret
	}

	log.Printf("已生成并保存会话密钥: %s（设置 SESSION_SECRET 可覆盖）", path)

	return secret
}

func randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("无法生成随机字节: %v", err)
	}

	return buf
}
