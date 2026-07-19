package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

var (
	username     string
	passwordHash string
	tokens       = make(map[string]time.Time)
	tokensMu     sync.RWMutex
	authFile     string // auth.json 路径，用于动态重载
	authFileMod  time.Time
	authMu       sync.RWMutex
)

// loadAuthFile 从 auth.json 重新加载密码
func loadAuthFile() {
	if authFile == "" {
		return
	}
	data, err := os.ReadFile(authFile)
	if err != nil {
		return
	}
	var cfg map[string]string
	if json.Unmarshal(data, &cfg) != nil {
		return
	}
	for u, h := range cfg {
		authMu.Lock()
		username = u
		if strings.HasPrefix(h, "$2a$") {
			passwordHash = h
		} else {
			hash, _ := bcrypt.GenerateFromPassword([]byte(h), bcrypt.DefaultCost)
			passwordHash = string(hash)
		}
		authMu.Unlock()
		break
	}
}

// InitAuth 初始化登录验证
// cred 格式: username:password（明文，启动时立即哈希）
// 如果 ~/.mutagen-web/auth.json 存在，优先从文件读取并动态监控
func InitAuth(cred string) {
	home, _ := os.UserHomeDir()
	authFile = filepath.Join(home, ".mutagen-web", "auth.json")

	// 尝试从文件读取
	if _, err := os.Stat(authFile); err == nil {
		loadAuthFile()
		// 记录文件修改时间
		if fi, err := os.Stat(authFile); err == nil {
			authFileMod = fi.ModTime()
		}
		// 启动定时重载（每 30 秒检查文件变化）
		go func() {
			for {
				time.Sleep(30 * time.Second)
				if fi, err := os.Stat(authFile); err == nil {
					if fi.ModTime().After(authFileMod) {
						authFileMod = fi.ModTime()
						loadAuthFile()
					}
				}
			}
		}()
		goto initDone
	}

	// 从命令行参数读取
	if cred != "" {
		parts := strings.SplitN(cred, ":", 2)
		if len(parts) != 2 {
			panic("invalid auth format, use username:password")
		}
		username = parts[0]
		hash, _ := bcrypt.GenerateFromPassword([]byte(parts[1]), bcrypt.DefaultCost)
		passwordHash = string(hash)
	}

initDone:
	// 定期清理过期 token
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			tokensMu.Lock()
			now := time.Now()
			for t, exp := range tokens {
				if now.After(exp) {
					delete(tokens, t)
				}
			}
			tokensMu.Unlock()
		}
	}()
}

// GeneratePasswordHash 生成 bcrypt 密码哈希
func GeneratePasswordHash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func generateToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// LoginHandler 登录接口（bcrypt 比对）
func LoginHandler(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	authMu.RLock()
	u := username
	h := passwordHash
	authMu.RUnlock()

	if req.Username != u {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(h), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}

	token := generateToken()
	tokensMu.Lock()
	tokens[token] = time.Now().Add(24 * time.Hour)
	tokensMu.Unlock()

	c.JSON(http.StatusOK, gin.H{"token": token})
}

// AuthCheck 验证 token
func AuthCheck(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("token")
	}
	if token == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	if strings.HasPrefix(token, "Bearer ") {
		token = token[7:]
	}

	tokensMu.RLock()
	exp, ok := tokens[token]
	tokensMu.RUnlock()

	if !ok || time.Now().After(exp) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token expired or invalid"})
		return
	}

	c.Next()
}

// PrintHash 生成密码哈希并打印
func PrintHash(password string) {
	hash, err := GeneratePasswordHash(password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(hash)
	os.Exit(0)
}