package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ieuforu/comic/internal/chat"
)

// 1. 核心指令：编译期将 web/dist 下的所有静态资源烧录进二进制中
//
//go:embed all:web/dist
var distFS embed.FS

// maxPersistedMessages bounds the database: anything older can never be replayed
// because the hub only keeps maxHistory in memory anyway.
const maxPersistedMessages = 2000

func main() {
	hub := chat.NewHub()
	go hub.Run()

	staticFS, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		log.Fatalf("无法加载嵌入静态资源: %v", err)
	}

	// The build id is derived from the embedded bytes, so it changes exactly when
	// the SPA does. It is injected into index.html and sent in every snapshot, which
	// is how an already-open page learns that a deploy happened.
	assets, err := chat.LoadStatic(staticFS)
	if err != nil {
		log.Fatalf("无法读取嵌入静态资源: %v", err)
	}

	hub.SetBuildID(assets.BuildID)

	if len(assets.IndexHTML) == 0 {
		log.Println("警告: web/dist/index.html 缺失，页面将返回 404（先运行 make frontend）")
	} else {
		log.Printf("前端 build: %s", assets.BuildID)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	stateDir := chat.StateDirectory()
	store := chat.NewSessionStore(stateDir)

	// Persistence and media are optional: if the database cannot be opened the chat
	// still runs, it just forgets messages on restart and cannot accept images.
	var history *chat.HistoryStore

	var media *chat.MediaStore

	if stateDir != "" {
		opened, err := chat.OpenHistory(filepath.Join(stateDir, "history.db"), maxPersistedMessages)
		if err != nil {
			log.Printf("警告: 历史持久化不可用（改为仅内存）: %v", err)
		} else {
			history = opened

			restored, lastSeq, err := history.LoadRecent(chat.RestoreLimit)
			if err != nil {
				log.Printf("警告: 读取历史失败: %v", err)
			} else {
				hub.Restore(restored, lastSeq)
				log.Printf("已恢复 %d 条历史消息（seq 至 %d）", len(restored), lastSeq)
			}

			hub.SetHistorySink(history)

			media, err = chat.NewMediaStore(history.DB(), filepath.Join(stateDir, "media"))
			if err != nil {
				log.Printf("警告: 图片功能不可用（改为纯文本）: %v", err)
			} else {
				log.Printf("图片上限 %d MB，保留 %d 天", media.MaxMB(), media.TTLDays())
			}
		}
	}

	store.OnNamesChanged(func(prefix, display string) {
		hub.BroadcastProfile(prefix, display)
	})

	srv := &http.Server{
		Addr: addr,
		Handler: chat.Routes(chat.Deps{
			Hub:       hub,
			Sessions:  store,
			Media:     media,
			StaticFS:  staticFS,
			IndexHTML: assets.IndexHTML,
		}),
		// Without a header timeout a client can hold a connection open by
		// dribbling bytes and slowly exhaust the listener.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		log.Println("收到退出信号，开始优雅关闭")

		// WebSocket 连接是 hijacked 的，Shutdown 不会等它们，必须先主动关闭。
		hub.Shutdown()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Println("HTTP 关闭超时:", err)
		}
	}()

	// Profiling lives on its own listener; nothing is exposed unless DEBUG_ADDR
	// is set (e.g. DEBUG_ADDR=127.0.0.1:6060).
	if debugAddr := os.Getenv("DEBUG_ADDR"); debugAddr != "" {
		go func() {
			debugServer := &http.Server{
				Addr:              debugAddr,
				Handler:           debugRoutes(hub),
				ReadHeaderTimeout: 5 * time.Second,
			}

			log.Printf("pprof 已开启: http://%s/debug/pprof/", debugAddr)

			if err := debugServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Println("debug listener 异常:", err)
			}
		}()
	}

	rateCfg := hub.RateLimit()
	log.Printf("频控: mode=%s limit=%.1f 条/秒 burst=%d", rateCfg.Mode, rateCfg.PerSec, rateCfg.Burst)
	log.Printf("独立单二进制服务已就绪: http://localhost%s", addr)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("服务异常退出:", err)
	}

	log.Println("已退出")
}
