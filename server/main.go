package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"mutagen-web/server/db"
	"mutagen-web/server/handlers"
	"mutagen-web/server/ws"

	"github.com/gin-gonic/gin"
)

func main() {
	var (
		addr       = flag.String("addr", ":8080", "server listen address")
		dbPath     = flag.String("db", "", "database file path")
		agentBin   = flag.String("agent-bin", "", "agent.exe path for pack download")
		auth       = flag.String("auth", "", "login credentials (username:password)")
		printHash  = flag.String("print-hash", "", "generate bcrypt hash of a password and exit")
	logFile   = flag.String("log", "", "log file path (default: stdout)")
	)
	flag.Parse()

	// 生成密码哈希并退出
	if *printHash != "" {
		handlers.PrintHash(*printHash)
	}

	if err := db.Init(*dbPath); err != nil {
		log.Fatalf("init database failed: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// 日志重定向到文件
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".mutagen-web")
	os.MkdirAll(logDir, 0755)
	if *logFile != "" {
		os.MkdirAll(filepath.Dir(*logFile), 0755)
		if f, err := os.OpenFile(*logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			log.SetOutput(f)
			gin.DefaultWriter = f
		}
	}

	// 登录验证（支持 auth.json 配置文件 或 -auth 命令行参数）
	handlers.InitAuth(*auth)
	hasAuth := true
	if *auth == "" {
		home, _ := os.UserHomeDir()
		if _, err := os.Stat(filepath.Join(home, ".mutagen-web", "auth.json")); err != nil {
			hasAuth = false
		}
	}
	if hasAuth {
		r.POST("/api/login", handlers.LoginHandler)
		// 用中间件只拦截 /api 路径（不影响前端静态文件）
		r.Use(func(c *gin.Context) {
			if len(c.Request.URL.Path) >= 4 && c.Request.URL.Path[:4] == "/api" &&
				c.Request.URL.Path != "/api/login" &&
				c.Request.URL.Path != "/api/health" {
				handlers.AuthCheck(c)
				if c.IsAborted() {
					return
				}
			}
			c.Next()
		})
	}

	// API routes
	handlers.RegisterMachineRoutes(r, hub, *agentBin)
	handlers.RegisterTaskRoutes(r, hub)
	handlers.RegisterConfigRoutes(r, hub)

	// WebSocket endpoint for agents
	r.GET("/ws/agent", func(c *gin.Context) {
		hub.HandleAgentWebSocket(c)
	})

	// Health check
	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Serve frontend static files if web/dist exists
	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)
	staticDirs := []string{
		filepath.Join(exeDir, "web"),
		filepath.Join(exeDir, "..", "web", "dist"),
		filepath.Join(exeDir, "..", "build", "web"),
	}
	for _, dir := range staticDirs {
		if _, err := os.Stat(dir); err == nil {
			r.Static("/assets", dir+"/assets")
			r.StaticFile("/", dir+"/index.html")
			r.NoRoute(func(c *gin.Context) {
				c.File(dir + "/index.html")
			})
			break
		}
	}

	log.Printf("Mutagen Web Server listening on %s", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
