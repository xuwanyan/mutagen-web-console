package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
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
	userPasswords = make(map[string]string) // username -> bcrypt hash
	tokens        = make(map[string]time.Time)
	tokensMu      sync.RWMutex
	authFile      string // auth.json 路径，用于动态重载
	authFileMod   time.Time
	authFileMu    sync.RWMutex // 保护 authFileMod 并发读写
	userMu        sync.RWMutex // 保护 userPasswords
	// dummyBcryptHash 用于用户名不存在时消耗等价时间，避免按响应耗时枚举用户名。
	dummyBcryptHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-timing-equalizer"), bcrypt.DefaultCost)
)

// isBcryptHash 判断字符串是否为 bcrypt 哈希（支持 $2a$、$2b$、$2y$ 前缀）
func isBcryptHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

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
	userMu.Lock()
	defer userMu.Unlock()
	// 清空旧用户，用新文件内容覆盖
	userPasswords = make(map[string]string, len(cfg))
	for u, h := range cfg {
		if isBcryptHash(h) {
			userPasswords[u] = h
		} else {
			// 纯文本密码，自动哈希
			hash, err := bcrypt.GenerateFromPassword([]byte(h), bcrypt.DefaultCost)
			if err == nil {
				userPasswords[u] = string(hash)
			}
		}
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
		authFileMu.Lock()
		if fi, err := os.Stat(authFile); err == nil {
			authFileMod = fi.ModTime()
		}
		authFileMu.Unlock()
		// 启动定时重载（每 30 秒检查文件变化）
		go func() {
			for {
				time.Sleep(30 * time.Second)
				authFileMu.Lock()
				fi, err := os.Stat(authFile)
				if err == nil {
					if fi.ModTime().After(authFileMod) {
						authFileMod = fi.ModTime()
						authFileMu.Unlock()
						loadAuthFile()
						continue
					}
				}
				authFileMu.Unlock()
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
		hash, _ := bcrypt.GenerateFromPassword([]byte(parts[1]), bcrypt.DefaultCost)
		userMu.Lock()
		userPasswords[parts[0]] = string(hash)
		userMu.Unlock()
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
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("crypto/rand.Read failed: %v", err)
	}
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

	userMu.RLock()
	h, ok := userPasswords[req.Username]
	userMu.RUnlock()

	if !ok {
		// 用户名不存在：跑一次 bcrypt 比对消耗等价时间，避免按响应
		// 耗时侧信道枚举有效用户名。结果无论对错都返回 401。
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(req.Password))
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