package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type AgentConfig struct {
	ServerURL string `json:"server"`
	Token     string `json:"token"`
	MachineID string `json:"machineId"`
	Name      string `json:"name"`
}

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	destDir := "C:\\mutagen"
	agentExe := "mutagen-web-agent.exe"

	fmt.Println("========================================")
	fmt.Println("  Mutagen Web Agent - 一键安装")
	fmt.Println("========================================")
	fmt.Println()

	cfgPath := filepath.Join(dir, "agent-config.json")
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("读取 agent-config.json 失败: %v", err)
	}
	var cfg AgentConfig
	json.Unmarshal(cfgData, &cfg)

	fmt.Print("[1/5] 创建 C:\\mutagen\\... ")
	os.MkdirAll(destDir, 0755)
	fmt.Println("OK")

	fmt.Println("[2/5] 复制文件...")
	for _, f := range []string{"mutagen.exe", "mutagen-agents.tar.gz", agentExe} {
		src := filepath.Join(dir, f)
		if _, err := os.Stat(src); err == nil {
			copyFile(src, filepath.Join(destDir, f))
			fmt.Printf("     %s\n", f)
		}
	}
	os.WriteFile(filepath.Join(destDir, "agent-config.json"), cfgData, 0644)
	fmt.Println("     agent-config.json")

	currUser := os.Getenv("USERNAME")
	fmt.Printf("[3/5] 注册 Windows 服务（以 %s 用户运行）\n", currUser)

	binPath := fmt.Sprintf("%s --config %s -log C:\\mutagen\\agent.log", filepath.Join(destDir, agentExe), filepath.Join(destDir, "agent-config.json"))

	fmt.Print("     请输入 Windows 密码: ")
	var password string
	fmt.Scanln(&password)

	exec.Command("sc", "stop", "MutagenWebAgent").Run()
	time.Sleep(1 * time.Second)

	// 最多重试 5 次（解决 pending deletion 需要等待的问题）
	for i := 0; i < 5; i++ {
		// 直接执行 sc create（和手动命令完全一致）
		cmdStr := fmt.Sprintf("sc create MutagenWebAgent binPath= \"%s\" start= auto obj= \".\\%s\" password= \"%s\"", binPath, currUser, password)
		out, err := exec.Command("cmd", "/c", cmdStr).CombinedOutput()
		if err == nil {
			fmt.Printf("     服务创建成功\n")
			goto created
		}
		fmt.Printf("     重试 %d/5: %s\n", i+1, string(out))
		time.Sleep(3 * time.Second)
	}
	log.Fatal("服务创建失败，请手动执行：\n" + fmt.Sprintf("  sc create MutagenWebAgent binPath= \"%s\" start= auto obj= \".\\%s\" password= \"***\"", binPath, currUser))

created:
	exec.Command("sc", "description", "MutagenWebAgent", "Mutagen Web Agent").Run()
	exec.Command("sc", "failure", "MutagenWebAgent", "reset=", "86400", "actions=", "restart/5000/restart/10000/restart/30000").Run()
	fmt.Println("  OK")

	fmt.Print("[4/5] 启动服务... ")
	exec.Command("sc", "start", "MutagenWebAgent").Run()
	fmt.Println("OK")

	fmt.Print("[5/5] 设置系统 PATH... ")
	exec.Command("setx", "/M", "PATH", fmt.Sprintf("%%PATH%%;%s", destDir)).Run()
	fmt.Println("OK")

	writeManageBat(destDir)

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("  安装完成！")
	fmt.Printf("  机器:     %s\n", cfg.Name)
	fmt.Printf("  服务器:   %s\n", cfg.ServerURL)
	fmt.Println("  服务:     MutagenWebAgent（开机自启）")
	fmt.Println("  管理:     C:\\mutagen\\manage.bat")
	fmt.Println("========================================")
	fmt.Scanln()
}

func writeManageBat(destDir string) {
	content := `@echo off
chcp 65001 >nul
echo ========================================
echo  Mutagen Web Agent - 管理
echo ========================================
echo.
echo 1) 启动服务
echo 2) 停止服务
echo 3) 重启服务
echo 4) 退出
echo.
set /p opt=请选择:
if "%opt%"=="1" goto start
if "%opt%"=="2" goto stop
if "%opt%"=="3" goto restart
exit /b

:start
sc start MutagenWebAgent
goto end

:stop
sc stop MutagenWebAgent
goto end

:restart
sc stop MutagenWebAgent
timeout /t 2 /nobreak >nul
sc start MutagenWebAgent
goto end

:end
echo.
pause
`
	os.WriteFile(filepath.Join(destDir, "manage.bat"), []byte(content), 0644)
}

func copyFile(src, dst string) {
	s, _ := os.Open(src)
	defer s.Close()
	d, _ := os.Create(dst)
	defer d.Close()
	io.Copy(d, s)
}

func init() {
	log.SetFlags(log.Ltime)
}